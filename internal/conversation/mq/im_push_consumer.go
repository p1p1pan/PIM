package mq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"pim/internal/conversation/model"
	pbgateway "pim/internal/gateway/pb"
	logmodel "pim/internal/log/model"
	"pim/internal/mq/kafka"
)

// handleImPushBatch 批量解码后 Pipeline GET ws:conn；对**不同接收者**并行 PushToConn（同一 ToUserID 仍按分区内顺序串行）。
func handleImPushBatch(ctx context.Context, rdb *redis.Client, pushClients map[string]pbgateway.PushServiceClient, producer *kafka.Producer, batch []*sarama.ConsumerMessage) error {
	if len(batch) == 0 {
		return nil
	}
	kms := make([]model.KafkaMessage, 0, len(batch))
	for _, msg := range batch {
		km, err := model.DecodeKafkaMessage(msg.Value)
		if err != nil {
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "error",
				Service:   "conversation",
				TraceID:   "unknown",
				Msg:       "im-message-push decode failed",
				ErrorCode: "decode_failed",
			})
			return err
		}
		if km.TraceID == "" {
			km.TraceID = "unknown"
		}
		if km.ClientMsgID == "" {
			km.ClientMsgID = uuid.NewString()
		}
		kms = append(kms, km)
	}
	if rdb == nil || len(pushClients) == 0 {
		return nil
	}

	ttl := routeCacheTTL()
	nowMs := time.Now().UnixMilli()

	routeVals := make([]string, len(kms))
	var misses []int

	for i := range kms {
		if ttl > 0 {
			if route, ok := getRouteFromCache(kms[i].ToUserID, nowMs); ok {
				routeVals[i] = route
				continue
			}
		}
		misses = append(misses, i)
	}

	if len(misses) > 0 {
		pipe := rdb.Pipeline()
		cmds := make([]*redis.StringCmd, len(misses))
		for i, idx := range misses {
			cmds[i] = pipe.Get(ctx, fmt.Sprintf("ws:conn:%d", kms[idx].ToUserID))
		}
		_, _ = pipe.Exec(ctx)
		for i, idx := range misses {
			val, err := cmds[i].Result()
			if err != nil || val == "" {
				continue
			}
			routeVals[idx] = val
			putRouteCache(kms[idx].ToUserID, val, ttl)
		}
	}

	// 仅处理在线条目；按 route 前缀分发到对应 gateway PushService。
	onlineByNode := make(map[string][]model.KafkaMessage)
	for i := range kms {
		val := routeVals[i]
		if val == "" {
			continue
		}
		parts := strings.SplitN(val, ":", 2)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		node := parts[0]
		if _, ok := pushClients[node]; !ok {
			continue
		}
		onlineByNode[node] = append(onlineByNode[node], kms[i])
	}
	if len(onlineByNode) == 0 {
		return nil
	}
	for node, online := range onlineByNode {
		client := pushClients[node]
		if client == nil {
			continue
		}
		items := make([]*pbgateway.PushBatchItem, 0, len(online))
		for _, km := range online {
			items = append(items, &pbgateway.PushBatchItem{
				FromUserId: uint64(km.FromUserID),
				ToUserId:   uint64(km.ToUserID),
				Content:    km.Content,
			})
		}
		resp, err := client.PushBatchToConn(ctx, &pbgateway.PushBatchToConnRequest{Items: items})
		if err != nil {
			// 兼容老 gateway（未升级 batch RPC）或临时网络异常：回退到旧单条路径。
			if strings.Contains(strings.ToLower(err.Error()), "unimplemented") {
				var pushFailed int
				for _, km := range online {
					if _, pushErr := client.PushToConn(ctx, &pbgateway.PushToConnRequest{
						FromUserId: uint64(km.FromUserID),
						ToUserId:   uint64(km.ToUserID),
						Content:    km.Content,
					}); pushErr != nil {
						pushFailed++
						emitConsumerLog(producer, logmodel.Log{
							TS:        time.Now(),
							Level:     "warn",
							Service:   "conversation",
							TraceID:   km.TraceID,
							Msg:       "im-message push failed",
							EventID:   km.ClientMsgID,
							UserID:    uint64(km.FromUserID),
							ErrorCode: "push_failed",
						})
					}
				}
				if pushFailed > 0 && ttl > 0 {
					for _, km := range online {
						pushRouteCache.Delete(km.ToUserID)
					}
				}
				continue
			}
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "warn",
				Service:   "conversation",
				TraceID:   online[0].TraceID,
				Msg:       "im-message push batch rpc failed",
				EventID:   online[0].ClientMsgID,
				UserID:    uint64(online[0].FromUserID),
				ErrorCode: "push_batch_failed",
			})
			return err
		}
		if resp.GetFailed() > 0 && ttl > 0 {
			// 批量返回失败时无法精准定位失败项，保守清理本批 toUser 缓存。
			for _, km := range online {
				pushRouteCache.Delete(km.ToUserID)
			}
		}
	}
	return nil
}

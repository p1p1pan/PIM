package mq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"pim/internal/conversation/model"
	pbgateway "pim/internal/gateway/pb"
	logmodel "pim/internal/log/model"
	"pim/internal/kit/mq/kafka"
	observemetrics "pim/internal/kit/observability/metrics"
	"pim/internal/registry"
)

// handleImPushBatch 批量解码后 Pipeline GET ws:conn，并按 gateway 节点聚合后发送批量推送。
// 注意：当前实现是“按节点顺序发送批次”，而非节点级并行；分区内消息顺序仍由消费模型保证。
func handleImPushBatch(ctx context.Context, rdb *redis.Client, pushLookup registry.GatewayPushClientLookup, producer *kafka.Producer, batch []*sarama.ConsumerMessage) error {
	if len(batch) == 0 {
		return nil
	}
	pushClients := pushLookup.Snapshot()
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
			observemetrics.ObserveConversationPushDropped("route_missing")
			continue
		}
		parts := strings.SplitN(val, ":", 2)
		if len(parts) < 2 || parts[0] == "" {
			observemetrics.ObserveConversationPushDropped("route_malformed")
			continue
		}
		node := parts[0]
		if _, ok := pushClients[node]; !ok {
			observemetrics.ObserveConversationPushDropped("node_not_found")
			continue
		}
		onlineByNode[node] = append(onlineByNode[node], kms[i])
	}
	if len(onlineByNode) == 0 {
		return nil
	}
	// 节点级并行：多 gateway 场景下，串行 gRPC 会把每个 node 的 RTT 线性累加；
	// 改成 errgroup 并行后，本批整体耗时 ≈ 最慢节点的耗时。
	g, gctx := errgroup.WithContext(ctx)
	if n := len(onlineByNode); n > 0 {
		g.SetLimit(n)
	}
	for node, online := range onlineByNode {
		node, online := node, online
		client := pushClients[node]
		if client == nil {
			continue
		}
		g.Go(func() error {
			items := make([]*pbgateway.PushBatchItem, 0, len(online))
			for _, km := range online {
				items = append(items, &pbgateway.PushBatchItem{
					FromUserId: uint64(km.FromUserID),
					ToUserId:   uint64(km.ToUserID),
					Content:    km.Content,
				})
			}
			resp, err := client.PushBatchToConn(gctx, &pbgateway.PushBatchToConnRequest{Items: items})
			if err != nil {
				// 兼容老 gateway（未升级 batch RPC）或临时网络异常：回退到旧单条路径；
				// 单条仍失败只记日志不抛错，避免一条"死信"级别失败把整批丢回 Kafka 造成风暴。
				if strings.Contains(strings.ToLower(err.Error()), "unimplemented") {
					var pushFailed int
					for _, km := range online {
						if _, pushErr := client.PushToConn(gctx, &pbgateway.PushToConnRequest{
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
					return nil
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
				// 批量返回失败时无法精准定位失败项，保守清理本节点分组下的 toUser 缓存。
				for _, km := range online {
					pushRouteCache.Delete(km.ToUserID)
				}
			}
			return nil
		})
	}
	return g.Wait()
}

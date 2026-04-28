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

// imPushItem 把解码后的消息与 sender 侧入口时间绑在一起，
// 便于在推送完成后以单条粒度观测 e2e 延迟。
type imPushItem struct {
	km          model.KafkaMessage
	ingressTsNs int64 // 0 表示未带 ingress_ts_ns header，不参与 e2e 观测
}

// handleImPushBatch 批量解码后 Pipeline GET ws:conn，并按 gateway 节点聚合后发送批量推送。
// 注意：当前实现是“按节点顺序发送批次”，而非节点级并行；分区内消息顺序仍由消费模型保证。
func handleImPushBatch(ctx context.Context, rdb *redis.Client, pushLookup registry.GatewayPushClientLookup, producer *kafka.Producer, batch []*sarama.ConsumerMessage) error {
	if len(batch) == 0 {
		return nil
	}
	pushClients := pushLookup.Snapshot()
	items := make([]imPushItem, 0, len(batch))
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
		ts, _ := kafka.ExtractIngressTsNsFromHeaders(msg.Headers)
		items = append(items, imPushItem{km: km, ingressTsNs: ts})
	}
	if rdb == nil || len(pushClients) == 0 {
		return nil
	}

	ttl := routeCacheTTL()
	nowMs := time.Now().UnixMilli()

	routeVals := make([]string, len(items))
	var misses []int

	for i := range items {
		if ttl > 0 {
			if route, ok := getRouteFromCache(items[i].km.ToUserID, nowMs); ok {
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
			cmds[i] = pipe.Get(ctx, fmt.Sprintf("ws:conn:%d", items[idx].km.ToUserID))
		}
		_, _ = pipe.Exec(ctx)
		for i, idx := range misses {
			val, err := cmds[i].Result()
			if err != nil || val == "" {
				continue
			}
			routeVals[idx] = val
			putRouteCache(items[idx].km.ToUserID, val, ttl)
		}
	}

	// 仅处理在线条目；按 route 前缀分发到对应 gateway PushService。
	onlineByNode := make(map[string][]imPushItem)
	for i := range items {
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
		onlineByNode[node] = append(onlineByNode[node], items[i])
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
			batchItems := make([]*pbgateway.PushBatchItem, 0, len(online))
			for _, it := range online {
				batchItems = append(batchItems, &pbgateway.PushBatchItem{
					FromUserId: uint64(it.km.FromUserID),
					ToUserId:   uint64(it.km.ToUserID),
					Content:    it.km.Content,
				})
			}
			resp, err := client.PushBatchToConn(gctx, &pbgateway.PushBatchToConnRequest{Items: batchItems})
			if err != nil {
				// 兼容老 gateway（未升级 batch RPC）或临时网络异常：回退到旧单条路径；
				// 单条仍失败只记日志不抛错，避免一条"死信"级别失败把整批丢回 Kafka 造成风暴。
				if strings.Contains(strings.ToLower(err.Error()), "unimplemented") {
					var pushFailed int
					for _, it := range online {
						if _, pushErr := client.PushToConn(gctx, &pbgateway.PushToConnRequest{
							FromUserId: uint64(it.km.FromUserID),
							ToUserId:   uint64(it.km.ToUserID),
							Content:    it.km.Content,
						}); pushErr != nil {
							pushFailed++
							emitConsumerLog(producer, logmodel.Log{
								TS:        time.Now(),
								Level:     "warn",
								Service:   "conversation",
								TraceID:   it.km.TraceID,
								Msg:       "im-message push failed",
								EventID:   it.km.ClientMsgID,
								UserID:    uint64(it.km.FromUserID),
								ErrorCode: "push_failed",
							})
						} else {
							observeImE2EIfSet(it.ingressTsNs)
						}
					}
					if pushFailed > 0 && ttl > 0 {
						for _, it := range online {
							pushRouteCache.Delete(it.km.ToUserID)
						}
					}
					return nil
				}
				emitConsumerLog(producer, logmodel.Log{
					TS:        time.Now(),
					Level:     "warn",
					Service:   "conversation",
					TraceID:   online[0].km.TraceID,
					Msg:       "im-message push batch rpc failed",
					EventID:   online[0].km.ClientMsgID,
					UserID:    uint64(online[0].km.FromUserID),
					ErrorCode: "push_batch_failed",
				})
				return err
			}
			// 批量成功：对每一条带有 ingress_ts_ns 的消息记录 e2e；
			// resp.Failed > 0 时批内部分失败具体是哪条无法精准定位，保守仅观测整体成功的情况下全量 e2e，
			// 偶发错误引入的小偏差可接受，不因此留白 histogram。
			for _, it := range online {
				observeImE2EIfSet(it.ingressTsNs)
			}
			if resp.GetFailed() > 0 && ttl > 0 {
				// 批量返回失败时无法精准定位失败项，保守清理本节点分组下的 toUser 缓存。
				for _, it := range online {
					pushRouteCache.Delete(it.km.ToUserID)
				}
			}
			return nil
		})
	}
	return g.Wait()
}

// observeImE2EIfSet 只在 sender 确实写入了 ingress_ts_ns 时观测 histogram，
// 避免老消息或异常导致的 now - 0 写爆上界。
func observeImE2EIfSet(ingressTsNs int64) {
	if ingressTsNs <= 0 {
		return
	}
	elapsed := time.Now().UnixNano() - ingressTsNs
	if elapsed < 0 {
		return
	}
	observemetrics.ObserveIMe2eServerSeconds("im-message", float64(elapsed)/1e9)
}

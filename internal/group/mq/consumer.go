package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Shopify/sarama"
	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	pbgateway "pim/internal/gateway/pb"
	groupmodel "pim/internal/group/model"
	grouprepo "pim/internal/group/repo"
	groupservice "pim/internal/group/service"
	logmodel "pim/internal/log/model"
	"pim/internal/mq/kafka"
)

// StartConsumers 启动 group-message 消费者。
func StartConsumers(ctx context.Context, svc *groupservice.Service, rdb *redis.Client, pushClients map[string]pbgateway.PushServiceClient, producer *kafka.Producer, brokers []string) {
	go func() {
		bs := config.KafkaGroupMessageBatchSize
		if bs <= 0 {
			bs = 64
		}
		bw := time.Duration(config.KafkaGroupMessageBatchWaitMs) * time.Millisecond
		if bw <= 0 {
			bw = 5 * time.Millisecond
		}
		err := kafka.StartConsumerGroupBatch(ctx, brokers, config.KafkaGroupMessageGroupID, []string{"group-message"}, bs, bw, func(ctx context.Context, msgs []*sarama.ConsumerMessage) error {
			return handleGroupMessageBatch(ctx, svc, rdb, pushClients, producer, msgs)
		})
		if err != nil {
			log.Printf("failed to start group-message consumer: %v", err)
		}
	}()
	go func() {
		err := kafka.StartConsumerGroup(ctx, brokers, config.KafkaGroupMemberSyncGroupID, []string{"group-member-sync"}, func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleGroupMemberSync(ctx, rdb, producer, msg)
		})
		if err != nil {
			log.Printf("failed to start group-member-sync consumer: %v", err)
		}
	}()
}

func handleGroupMessageBatch(ctx context.Context, svc *groupservice.Service, rdb *redis.Client, pushClients map[string]pbgateway.PushServiceClient, producer *kafka.Producer, msgs []*sarama.ConsumerMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	parallel := config.KafkaGroupMessageBatchParallel
	if parallel <= 1 || len(msgs) == 1 {
		for _, msg := range msgs {
			if err := handleGroupMessage(ctx, svc, rdb, pushClients, producer, msg); err != nil {
				return err
			}
		}
		return nil
	}

	// 分区内：同 group 要保序；不同 group 可并行。
	groups := make(map[uint][]groupmodel.GroupKafkaMessage, len(msgs))
	order := make([]uint, 0, len(msgs))
	traceByGroup := make(map[uint]string, len(msgs))
	for _, msg := range msgs {
		km, mode, err := groupmodel.DecodeGroupKafkaMessageWithMode(msg.Value)
		if err != nil {
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "error",
				Service:   "group",
				TraceID:   "unknown",
				Msg:       "group-message decode failed",
				ErrorCode: "decode_failed",
			})
			return err
		}
		if mode == groupmodel.GroupKafkaDecodeJSON {
			// JSON 兜底仅用于灰度兼容，稳定后应关闭 fallback。
			fallbackN := groupmodel.GroupKafkaJSONFallbackCount()
			if fallbackN <= 5 || fallbackN%1000 == 0 {
				log.Printf("group-message json fallback decode used count=%d", fallbackN)
			}
		}
		if _, ok := groups[km.GroupID]; !ok {
			order = append(order, km.GroupID)
		}
		groups[km.GroupID] = append(groups[km.GroupID], km)
		if traceByGroup[km.GroupID] == "" && km.TraceID != "" {
			traceByGroup[km.GroupID] = km.TraceID
		}
	}

	sem := make(chan struct{}, parallel)
	errCh := make(chan error, len(groups))
	var wg sync.WaitGroup
	for _, gid := range order {
		batch := groups[gid]
		traceID := traceByGroup[gid]
		wg.Add(1)
		go func(groupID uint, list []groupmodel.GroupKafkaMessage, traceID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			memberIDs, err := getMemberIDsWithCache(svc, groupID)
			if err != nil {
				errCh <- err
				return
			}
			routeByUID, err := loadGroupRoutes(ctx, rdb, memberIDs)
			if err != nil {
				// 路由查询失败不阻断落库，仅记日志继续后续离线可见语义。
				log.Printf("group route load failed gid=%d: %v", groupID, err)
				routeByUID = map[uint64]string{}
			}
			inputs := make([]grouprepo.SaveGroupMessageInput, 0, len(list))
			for _, km := range list {
				inputs = append(inputs, grouprepo.SaveGroupMessageInput{
					GroupID:    km.GroupID,
					FromUserID: km.From,
					Content:    km.Content,
					EventID:    km.EventID,
				})
			}
			savedMsgs, err := svc.SaveIncomingGroupMessageBatchTrusted(groupID, inputs)
			if err != nil {
				errCh <- err
				return
			}
			// 同 group 内消息顺序由 savedMsgs 顺序保证；这里按批聚合推送，显著减少 gRPC 往返。
			pushSavedGroupMessagesBatch(ctx, pushClients, producer, traceID, list, savedMsgs, routeByUID)
		}(gid, batch, traceID)
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

func handleGroupMemberSync(ctx context.Context, rdb *redis.Client, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	if rdb == nil {
		return nil
	}
	var evt groupmodel.GroupMemberSyncEvent
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "group",
			TraceID:   "unknown",
			Msg:       "group-member-sync decode failed",
			ErrorCode: "decode_failed",
		})
		return err
	}
	traceID := evt.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	key := fmt.Sprintf("group:members:%d", evt.GroupID)
	readyKey := fmt.Sprintf("group:members:ready:%d", evt.GroupID)
	pipe := rdb.Pipeline()
	switch evt.Op {
	case "delete":
		pipe.Del(ctx, key, readyKey)
	case "snapshot":
		pipe.Del(ctx, key)
		if len(evt.MemberUserIDs) > 0 {
			args := make([]interface{}, 0, len(evt.MemberUserIDs))
			for _, uid := range evt.MemberUserIDs {
				args = append(args, uid)
			}
			pipe.SAdd(ctx, key, args...)
		}
		pipe.Set(ctx, readyKey, "1", 0)
	default:
		return nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "warn",
			Service:   "group",
			TraceID:   traceID,
			Msg:       "group-member-sync apply failed",
			GroupID:   evt.GroupID,
			ErrorCode: "apply_failed",
		})
		return err
	}
	return nil
}

// handleGroupMessage 处理群消息事件：校验并落库，随后向在线成员扇出。
// 当前实现只向本节点连接做实时推送，跨节点由 ws:conn 的 gateway 前缀做归属判断。
func handleGroupMessage(ctx context.Context, svc *groupservice.Service, rdb *redis.Client, pushClients map[string]pbgateway.PushServiceClient, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	km, mode, err := groupmodel.DecodeGroupKafkaMessageWithMode(msg.Value)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "group",
			TraceID:   "unknown",
			Msg:       "group-message decode failed",
			ErrorCode: "decode_failed",
		})
		return err
	}
	if mode == groupmodel.GroupKafkaDecodeJSON {
		fallbackN := groupmodel.GroupKafkaJSONFallbackCount()
		if fallbackN <= 5 || fallbackN%1000 == 0 {
			log.Printf("group-message json fallback decode used count=%d", fallbackN)
		}
	}
	traceID := km.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	// 成员身份已在网关写入 Kafka 前校验，这里走 trusted 路径避免重复 IsMember 查询。
	saved, err := svc.SaveIncomingGroupMessageTrusted(km.GroupID, km.From, km.Content, km.EventID)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "group",
			TraceID:   traceID,
			Msg:       "group-message persist failed",
			EventID:   km.EventID,
			UserID:    uint64(km.From),
			GroupID:   uint64(km.GroupID),
			ErrorCode: "persist_failed",
		})
		return err
	}
	if rdb == nil || len(pushClients) == 0 {
		return nil
	}
	memberIDs, err := getMemberIDsWithCache(svc, km.GroupID)
	if err != nil {
		return err
	}
	// 复用 PushToConn 的 content 字段承载群消息 JSON 事件体。
	pushBody, _ := json.Marshal(groupmodel.GroupPushMessage{
		Type:        "group_message",
		GroupID:     saved.GroupID,
		FromUserID:  saved.FromUserID,
		MessageType: saved.MessageType,
		Content:     saved.Content,
		Seq:         saved.Seq,
	})
	routeByUID, err := loadGroupRoutes(ctx, rdb, memberIDs)
	if err != nil {
		// 路由失败不影响落库语义，仅跳过在线推送。
		log.Printf("group route load failed gid=%d: %v", km.GroupID, err)
		routeByUID = map[uint64]string{}
	}

	items := make([]*pbgateway.PushBatchItem, 0, len(routeByUID))
	byNode := make(map[string][]*pbgateway.PushBatchItem)
	for uid, route := range routeByUID {
		parts := strings.SplitN(route, ":", 2)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		node := parts[0]
		if _, ok := pushClients[node]; !ok {
			continue
		}
		item := &pbgateway.PushBatchItem{
			FromUserId: uint64(km.From),
			ToUserId:   uid,
			Content:    string(pushBody),
		}
		items = append(items, item)
		byNode[node] = append(byNode[node], item)
	}
	if len(items) > 0 {
		for node, nodeItems := range byNode {
			client := pushClients[node]
			if client == nil {
				continue
			}
			_, pushErr := client.PushBatchToConn(ctx, &pbgateway.PushBatchToConnRequest{Items: nodeItems})
			if pushErr != nil {
				// 回退到逐条推，保证兼容旧网关。
				for _, it := range nodeItems {
					_, err := client.PushToConn(ctx, &pbgateway.PushToConnRequest{
						FromUserId:   it.GetFromUserId(),
						ToUserId:     it.GetToUserId(),
						Content:      it.GetContent(),
						ContentBytes: it.GetContentBytes(),
					})
					if err != nil {
						groupPushRouteCache.Delete(it.GetToUserId())
					}
				}
				emitConsumerLog(producer, logmodel.Log{
					TS:        time.Now(),
					Level:     "warn",
					Service:   "group",
					TraceID:   traceID,
					Msg:       "group-message push failed",
					EventID:   km.EventID,
					UserID:    uint64(km.From),
					GroupID:   uint64(km.GroupID),
					ErrorCode: "push_failed",
				})
			}
		}
	}
	emitConsumerLog(producer, logmodel.Log{
		TS:      time.Now(),
		Level:   "info",
		Service: "group",
		TraceID: traceID,
		Msg:     "group-message consumed",
		EventID: km.EventID,
		UserID:  uint64(km.From),
		GroupID: uint64(km.GroupID),
	})
	return nil
}

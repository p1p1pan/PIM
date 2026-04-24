package mq

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"pim/internal/config"
	pbgateway "pim/internal/gateway/pb"
	groupmodel "pim/internal/group/model"
	logmodel "pim/internal/log/model"
	"pim/internal/kit/mq/kafka"
	"pim/internal/registry"
)

// pushSavedGroupMessagesBatch 将同 group 批量落库结果按 node 分发到 gateway。
// 并发语义：
// - 同 node 使用单 worker 顺序发送，避免同连接乱序；
// - 不同 node 并行发送，提升跨节点推送吞吐；
// - 批量 RPC 失败后降级逐条推送，并清理失败用户路由缓存。
func pushSavedGroupMessagesBatch(
	ctx context.Context,
	pushLookup registry.GatewayPushClientLookup,
	producer *kafka.Producer,
	traceID string,
	list []groupmodel.GroupKafkaMessage,
	savedMsgs []groupmodel.GroupMessage,
	routeByUID map[uint64]string,
) {
	pushClients := pushLookup.Snapshot()
	if len(list) == 0 || len(savedMsgs) == 0 || len(routeByUID) == 0 || len(pushClients) == 0 {
		return
	}
	maxItems := config.KafkaGroupPushRPCItemsMax
	if maxItems <= 0 {
		maxItems = 2000
	}
	// 先把 uid -> node 解析一次，避免在每条消息上重复 split/查找。
	recipientsByNode := make(map[string][]uint64, len(pushClients))
	for uid, route := range routeByUID {
		parts := strings.SplitN(route, ":", 2)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		node := parts[0]
		if _, ok := pushClients[node]; !ok {
			continue
		}
		recipientsByNode[node] = append(recipientsByNode[node], uid)
	}
	if len(recipientsByNode) == 0 {
		return
	}

	// 每个 node 一个顺序 worker：同 node 保序，不同 node 并行。
	type nodeTask struct {
		items []*pbgateway.PushBatchItem
	}
	nodeCh := make(map[string]chan nodeTask, len(recipientsByNode))
	var nodeWG sync.WaitGroup
	for node := range recipientsByNode {
		client := pushClients[node]
		if client == nil {
			continue
		}
		ch := make(chan nodeTask, 8)
		nodeCh[node] = ch
		nodeWG.Add(1)
		go func(node string, client pbgateway.PushServiceClient, taskCh <-chan nodeTask) {
			defer nodeWG.Done()
			for task := range taskCh {
				batch := task.items
				if len(batch) == 0 {
					continue
				}
				if _, err := client.PushBatchToConn(ctx, &pbgateway.PushBatchToConnRequest{Items: batch}); err != nil {
					for _, it := range batch {
						_, oneErr := client.PushToConn(ctx, &pbgateway.PushToConnRequest{
							FromUserId:   it.GetFromUserId(),
							ToUserId:     it.GetToUserId(),
							Content:      it.GetContent(),
							ContentBytes: it.GetContentBytes(),
						})
						if oneErr != nil {
							groupPushRouteCache.Delete(it.GetToUserId())
						}
					}
					emitConsumerLog(producer, logmodel.Log{
						TS:        time.Now(),
						Level:     "warn",
						Service:   "group",
						TraceID:   traceID,
						Msg:       "group-message batch push failed",
						ErrorCode: "push_failed",
					})
				}
			}
		}(node, client, ch)
	}
	defer func() {
		for _, ch := range nodeCh {
			close(ch)
		}
		nodeWG.Wait()
	}()
	sendBatch := func(node string, batch []*pbgateway.PushBatchItem) {
		if len(batch) == 0 {
			return
		}
		ch, ok := nodeCh[node]
		if !ok {
			return
		}
		copied := make([]*pbgateway.PushBatchItem, len(batch))
		copy(copied, batch)
		ch <- nodeTask{items: copied}
	}
	byNode := make(map[string][]*pbgateway.PushBatchItem, len(recipientsByNode))
	for node, uids := range recipientsByNode {
		capHint := len(uids)
		if capHint > maxItems {
			capHint = maxItems
		}
		byNode[node] = make([]*pbgateway.PushBatchItem, 0, capHint)
	}
	for _, saved := range savedMsgs {
		pushJSON, _ := json.Marshal(groupmodel.GroupPushMessage{
			Type:        "group_message",
			GroupID:     saved.GroupID,
			FromUserID:  saved.FromUserID,
			MessageType: saved.MessageType,
			Content:     saved.Content,
			Seq:         saved.Seq,
			MentionMeta: saved.MentionMeta,
		})
		pushJSONStr := string(pushJSON)
		for node, uids := range recipientsByNode {
			buf := byNode[node]
			for _, uid := range uids {
				buf = append(buf, &pbgateway.PushBatchItem{
					FromUserId: uint64(saved.FromUserID),
					ToUserId:   uid,
					Content:    pushJSONStr,
				})
				if len(buf) >= maxItems {
					sendBatch(node, buf[:maxItems])
					buf = buf[maxItems:]
				}
			}
			byNode[node] = buf
		}
	}
	for node, rest := range byNode {
		sendBatch(node, rest)
	}
}

// pushSavedGroupMessage 保留给单消息路径的兼容发送实现。
func pushSavedGroupMessage(ctx context.Context, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, traceID string, km groupmodel.GroupKafkaMessage, saved groupmodel.GroupMessage, routeByUID map[uint64]string) error {
	pushJSON, _ := json.Marshal(groupmodel.GroupPushMessage{
		Type:        "group_message",
		GroupID:     saved.GroupID,
		FromUserID:  saved.FromUserID,
		MessageType: saved.MessageType,
		Content:     saved.Content,
		Seq:         saved.Seq,
		MentionMeta: saved.MentionMeta,
	})
	items := make([]*pbgateway.PushBatchItem, 0, len(routeByUID))
	for uid, route := range routeByUID {
		if !strings.HasPrefix(route, config.GatewayNodeID+":") {
			continue
		}
		items = append(items, &pbgateway.PushBatchItem{
			FromUserId: uint64(km.From),
			ToUserId:   uid,
			Content:    string(pushJSON),
		})
	}
	if len(items) == 0 {
		return nil
	}
	_, pushErr := pushClient.PushBatchToConn(ctx, &pbgateway.PushBatchToConnRequest{Items: items})
	if pushErr == nil {
		return nil
	}
	for _, it := range items {
		_, err := pushClient.PushToConn(ctx, &pbgateway.PushToConnRequest{
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
	return pushErr
}

package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"github.com/redis/go-redis/v9"

	pbgateway "pim/internal/gateway/pb"
	groupmodel "pim/internal/group/model"
	groupservice "pim/internal/group/service"
	logmodel "pim/internal/log/model"
	"pim/internal/mq/kafka"
)

// StartConsumers 启动 group-message 消费者。
func StartConsumers(ctx context.Context, svc *groupservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, brokers []string) {
	go func() {
		err := kafka.StartSimpleConsumer(ctx, brokers, "group-message", func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleGroupMessage(ctx, svc, rdb, pushClient, producer, msg)
		})
		if err != nil {
			log.Printf("failed to start group-message consumer: %v", err)
		}
	}()
}

// handleGroupMessage 处理群消息事件：校验并落库，随后向在线成员扇出。
// 当前实现只向本节点连接做实时推送，跨节点由 ws:conn 的 gateway 前缀做归属判断。
func handleGroupMessage(ctx context.Context, svc *groupservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	var km groupmodel.GroupKafkaMessage
	if err := json.Unmarshal(msg.Value, &km); err != nil {
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
	traceID := km.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	saved, err := svc.SaveIncomingGroupMessage(km.GroupID, km.From, km.Content, km.EventID)
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
	if rdb == nil || pushClient == nil {
		return nil
	}
	memberIDs, err := svc.ListMemberUserIDs(km.GroupID)
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
	for _, uid := range memberIDs {
		// 读取在线连接归属；不在线或无归属则只保留离线历史，不做实时推送。
		key := fmt.Sprintf("ws:conn:%d", uid)
		val, err := rdb.Get(ctx, key).Result()
		if err != nil || val == "" {
			continue
		}
		// 当前网关单节点约定：gateway-1。
		if !strings.HasPrefix(val, "gateway-1:") {
			continue
		}
		_, pushErr := pushClient.PushToConn(ctx, &pbgateway.PushToConnRequest{
			FromUserId: uint64(km.From),
			ToUserId:   uint64(uid),
			Content:    string(pushBody),
		})
		if pushErr != nil {
			// 推送失败不回滚落库，保证“消息已持久化”与“实时可达”解耦。
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

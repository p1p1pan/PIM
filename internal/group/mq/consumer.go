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

	"pim/internal/config"
	pbgateway "pim/internal/gateway/pb"
	groupmodel "pim/internal/group/model"
	groupservice "pim/internal/group/service"
	logkit "pim/internal/log/kit"
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
func handleGroupMessage(ctx context.Context, svc *groupservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	var km groupmodel.GroupKafkaMessage
	// 反序列化群消息事件
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
	// 落库群消息
	saved, err := svc.SaveIncomingGroupMessage(km.GroupID, km.From, km.Content, km.EventID)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "group",
			TraceID:   km.TraceID,
			Msg:       "group-message persist failed",
			EventID:   km.EventID,
			UserID:    uint64(km.From),
			GroupID:   uint64(km.GroupID),
			ErrorCode: "persist_failed",
		})
		return err
	}
	// 推送群消息给在线成员
	if rdb == nil || pushClient == nil {
		return nil
	}
	// 获取群成员 user_id 列表
	memberIDs, err := svc.ListMemberUserIDs(km.GroupID)
	if err != nil {
		return err
	}
	// 组装下行事件体（复用 PushToConn 的 content 字段承载 JSON）
	pushBody, _ := json.Marshal(groupmodel.GroupPushMessage{
		Type:        "group_message",
		GroupID:     saved.GroupID,
		FromUserID:  saved.FromUserID,
		MessageType: saved.MessageType,
		Content:     saved.Content,
		Seq:         saved.Seq,
	})
	// 推送群消息给在线成员
	for _, uid := range memberIDs {
		key := fmt.Sprintf("ws:conn:%d", uid)
		val, err := rdb.Get(ctx, key).Result()
		if err != nil || val == "" {
			continue
		}
		// 当前网关单节点约定：gateway-1
		if !strings.HasPrefix(val, "gateway-1:") {
			continue
		}
		// 推送群消息给在线成员
		_, pushErr := pushClient.PushToConn(ctx, &pbgateway.PushToConnRequest{
			FromUserId: uint64(km.From),
			ToUserId:   uint64(uid),
			Content:    string(pushBody),
		})
		if pushErr != nil {
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "warn",
				Service:   "group",
				TraceID:   km.TraceID,
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
		TraceID: km.TraceID,
		Msg:     "group-message consumed",
		EventID: km.EventID,
		UserID:  uint64(km.From),
		GroupID: uint64(km.GroupID),
	})
	return nil
}

func emitConsumerLog(producer *kafka.Producer, entry logmodel.Log) {
	if producer == nil {
		return
	}
	entry, ok := logkit.ApplyPolicy(entry, config.LogInfoSamplePct)
	if !ok {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = producer.SendMessage(context.Background(), "log-topic", "", data)
}

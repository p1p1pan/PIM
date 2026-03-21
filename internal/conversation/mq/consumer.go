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
	"pim/internal/conversation/model"
	pbgateway "pim/internal/gateway/pb"
	logkit "pim/internal/log/kit"
	logmodel "pim/internal/log/model"
	"pim/internal/mq/kafka"

	conversationservice "pim/internal/conversation/service"
)

// StartConsumers 启动会话相关 Kafka 消费者。
func StartConsumers(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, brokers []string) {
	go func() {
		err := kafka.StartSimpleConsumer(ctx, brokers, "im-message", func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleImMessage(ctx, svc, rdb, pushClient, producer, msg)
		})
		if err != nil {
			log.Printf("failed to start im-message consumer: %v", err)
		}
	}()
	go func() {
		err := kafka.StartSimpleConsumer(ctx, brokers, "im-message-read", func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleImReadMessage(ctx, svc, rdb, producer, msg)
		})
		if err != nil {
			log.Printf("failed to start im-message-read consumer: %v", err)
		}
	}()
}

// handleImMessage 处理单聊消息事件：落库、未读+1、在线推送。
func handleImMessage(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	var km model.KafkaMessage
	if err := json.Unmarshal(msg.Value, &km); err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:      time.Now(),
			Level:   "error",
			Service: "conversation",
			TraceID: "unknown",
			Msg:     "im-message decode failed",
			EventID: "",
			ErrorCode: "decode_failed",
		})
		return err
	}
	traceID := km.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	saved, _, err := svc.SendMessageIdempotent(km.FromUserID, km.ToUserID, km.Content, km.ClientMsgID)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "conversation",
			TraceID:   traceID,
			Msg:       "im-message persist failed",
			EventID:   km.ClientMsgID,
			UserID:    uint64(km.FromUserID),
			ErrorCode: "persist_failed",
		})
		return err
	}
	if rdb != nil {
		userA, userB := km.FromUserID, km.ToUserID
		if userA > userB {
			userA, userB = userB, userA
		}
		convKey := fmt.Sprintf("%d:%d", userA, userB)
		unreadKey := fmt.Sprintf("msg:unread:%d:%s", km.ToUserID, convKey)
		_ = rdb.Incr(ctx, unreadKey).Err()

		key := fmt.Sprintf("ws:conn:%d", km.ToUserID)
		val, err := rdb.Get(ctx, key).Result()
		if err == nil && val != "" {
			if !strings.HasPrefix(val, "gateway-1:") {
				return nil
			}
			if pushClient != nil {
				_, pushErr := pushClient.PushToConn(ctx, &pbgateway.PushToConnRequest{
					FromUserId: uint64(km.FromUserID),
					ToUserId:   uint64(km.ToUserID),
					Content:    km.Content,
				})
				if pushErr != nil {
					emitConsumerLog(producer, logmodel.Log{
						TS:        time.Now(),
						Level:     "warn",
						Service:   "conversation",
						TraceID:   traceID,
						Msg:       "im-message push failed",
						EventID:   km.ClientMsgID,
						UserID:    uint64(km.FromUserID),
						ErrorCode: "push_failed",
					})
				}
			}
		}
	}
	log.Printf("[trace=%s] im-message handled msgID=%d", traceID, saved.ID)
	emitConsumerLog(producer, logmodel.Log{
		Level:          "info",
		Service:        "conversation",
		TraceID:        traceID,
		Msg:            "im-message consumed",
		EventID:        km.ClientMsgID,
		UserID:         uint64(km.FromUserID),
		ConversationID: fmt.Sprintf("%d:%d", minUint(km.FromUserID, km.ToUserID), maxUint(km.FromUserID, km.ToUserID)),
	})
	return nil
}

// handleImReadMessage 处理已读事件：推进游标并清理未读键。
func handleImReadMessage(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	var rm model.ImReadKafkaMessage
	if err := json.Unmarshal(msg.Value, &rm); err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "conversation",
			TraceID:   "unknown",
			Msg:       "im-message-read decode failed",
			ErrorCode: "decode_failed",
		})
		return err
	}
	if rm.UserID == 0 || rm.PeerID == 0 || rm.ConversationID == "" {
		return nil
	}
	_, err := svc.HandleReadEvent(rm.UserID, rm.ConversationID)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:             time.Now(),
			Level:          "error",
			Service:        "conversation",
			TraceID:        rm.TraceID,
			Msg:            "im-message-read handle failed",
			EventID:        rm.ConversationID,
			UserID:         uint64(rm.UserID),
			ConversationID: rm.ConversationID,
			ErrorCode:      "read_handle_failed",
		})
		return err
	}
	if rdb != nil {
		unreadKey := fmt.Sprintf("msg:unread:%d:%s", rm.UserID, rm.ConversationID)
		_ = rdb.Del(ctx, unreadKey).Err()
	}
	emitConsumerLog(producer, logmodel.Log{
		Level:          "info",
		Service:        "conversation",
		TraceID:        rm.TraceID,
		Msg:            "im-message-read consumed",
		EventID:        rm.ConversationID,
		UserID:         uint64(rm.UserID),
		ConversationID: rm.ConversationID,
	})
	return nil
}

func emitConsumerLog(producer *kafka.Producer, entry logmodel.Log) {
	if producer == nil {
		return
	}
	if entry.TS.IsZero() {
		entry.TS = time.Now()
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

func minUint(a, b uint) uint {
	if a < b {
		return a
	}
	return b
}

func maxUint(a, b uint) uint {
	if a > b {
		return a
	}
	return b
}


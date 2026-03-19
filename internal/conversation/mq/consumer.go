package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/Shopify/sarama"
	"github.com/redis/go-redis/v9"

	"pim/internal/conversation/model"
	pbgateway "pim/internal/gateway/pb"
	"pim/internal/mq/kafka"

	conversationservice "pim/internal/conversation/service"
)

// StartConsumers 启动会话相关 Kafka 消费者。
func StartConsumers(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, brokers []string) {
	go func() {
		err := kafka.StartSimpleConsumer(ctx, brokers, "im-message", func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleImMessage(ctx, svc, rdb, pushClient, msg)
		})
		if err != nil {
			log.Printf("failed to start im-message consumer: %v", err)
		}
	}()
	go func() {
		err := kafka.StartSimpleConsumer(ctx, brokers, "im-message-read", func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleImReadMessage(ctx, svc, rdb, msg)
		})
		if err != nil {
			log.Printf("failed to start im-message-read consumer: %v", err)
		}
	}()
}

// handleImMessage 处理单聊消息事件：落库、未读+1、在线推送。
func handleImMessage(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, msg *sarama.ConsumerMessage) error {
	var km model.KafkaMessage
	if err := json.Unmarshal(msg.Value, &km); err != nil {
		return err
	}
	traceID := km.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	saved, _, err := svc.SendMessageIdempotent(km.FromUserID, km.ToUserID, km.Content, km.ClientMsgID)
	if err != nil {
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
				_, _ = pushClient.PushToConn(ctx, &pbgateway.PushToConnRequest{
					FromUserId: uint64(km.FromUserID),
					ToUserId:   uint64(km.ToUserID),
					Content:    km.Content,
				})
			}
		}
	}
	log.Printf("[trace=%s] im-message handled msgID=%d", traceID, saved.ID)
	return nil
}

// handleImReadMessage 处理已读事件：推进游标并清理未读键。
func handleImReadMessage(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, msg *sarama.ConsumerMessage) error {
	var rm model.ImReadKafkaMessage
	if err := json.Unmarshal(msg.Value, &rm); err != nil {
		return err
	}
	if rm.UserID == 0 || rm.PeerID == 0 || rm.ConversationID == "" {
		return nil
	}
	_, err := svc.HandleReadEvent(rm.UserID, rm.ConversationID)
	if err != nil {
		return err
	}
	if rdb != nil {
		unreadKey := fmt.Sprintf("msg:unread:%d:%s", rm.UserID, rm.ConversationID)
		_ = rdb.Del(ctx, unreadKey).Err()
	}
	return nil
}

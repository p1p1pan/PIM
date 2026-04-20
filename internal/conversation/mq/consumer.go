package mq

import (
	"context"
	"log"
	"time"

	"github.com/Shopify/sarama"
	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	"pim/internal/kit/mq/kafka"
	"pim/internal/registry"

	conversationservice "pim/internal/conversation/service"
)

// StartConsumers 启动会话域三条消费链路：
// 1) im-message: 单聊上行事件，负责落库与触发推送事件；
// 2) im-message-read: 已读事件，负责推进读游标；
// 3) im-message-push: 推送事件，按在线路由下发到对应 gateway。
// 三条链路彼此解耦，任一链路抖动不会直接阻塞其它 topic 的消费。
func StartConsumers(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, pushLookup registry.GatewayPushClientLookup, producer *kafka.Producer, brokers []string) {
	go func() {
		batchSize := config.KafkaConversationIMBatchSize
		if batchSize <= 0 {
			batchSize = 1
		}
		batchWait := time.Duration(config.KafkaConversationIMBatchWaitMs) * time.Millisecond
		err := kafka.StartConsumerGroupBatch(ctx, brokers, config.KafkaConversationIMGroupID, []string{"im-message"}, batchSize, batchWait, func(ctx context.Context, msgs []*sarama.ConsumerMessage) error {
			return handleImMessageBatch(ctx, svc, rdb, producer, msgs)
		})
		if err != nil {
			log.Printf("failed to start im-message consumer: %v", err)
		}
	}()
	go func() {
		err := kafka.StartConsumerGroup(ctx, brokers, config.KafkaConversationReadGroupID, []string{"im-message-read"}, func(ctx context.Context, msg *sarama.ConsumerMessage) error {
			return handleImReadMessage(ctx, svc, rdb, producer, msg)
		})
		if err != nil {
			log.Printf("failed to start im-message-read consumer: %v", err)
		}
	}()
	go func() {
		ps := config.KafkaConversationPushBatchSize
		if ps <= 0 {
			ps = 1
		}
		pw := time.Duration(config.KafkaConversationPushBatchWaitMs) * time.Millisecond
		err := kafka.StartConsumerGroupBatch(ctx, brokers, config.KafkaConversationPushGroupID, []string{"im-message-push"}, ps, pw, func(ctx context.Context, msgs []*sarama.ConsumerMessage) error {
			return handleImPushBatch(ctx, rdb, pushLookup, producer, msgs)
		})
		if err != nil {
			log.Printf("failed to start im-message-push consumer: %v", err)
		}
	}()
}

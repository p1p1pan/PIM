package mq

import (
	"context"
	"log"

	"github.com/Shopify/sarama"
	"github.com/redis/go-redis/v9"

	pbgateway "pim/internal/gateway/pb"
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

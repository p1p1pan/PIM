package kafka

import (
	"context"
	"log"
	"time"

	"github.com/Shopify/sarama"
	observemetrics "pim/internal/observability/metrics"
)

// MessageHandler 用于处理消费到的消息。
type MessageHandler func(ctx context.Context, msg *sarama.ConsumerMessage) error

// StartSimpleConsumer 启动一个简单消费者，直接消费 topic 全部分区。
// 注意：该实现不维护 consumer group 与 offset 提交，更适合当前单消费者场景。
func StartSimpleConsumer(ctx context.Context, brokers []string, topic string, handler MessageHandler) error {
	config := sarama.NewConfig()
	config.Consumer.Return.Errors = true

	// 创建消费者
	consumer, err := sarama.NewConsumer(brokers, config)
	if err != nil {
		return err
	}
	// 关闭消费者
	go func() {
		<-ctx.Done()
		if err := consumer.Close(); err != nil {
			log.Printf("kafka consumer close error: %v", err)
		}
	}()
	// 获取分区
	partitions, err := consumer.Partitions(topic)
	if err != nil {
		return err
	}
	log.Printf("kafka: consuming topic %s from partitions %v", topic, partitions)
	// 消费分区
	for _, p := range partitions {
		// OffsetNewest: 仅消费新消息，避免本地联调时历史消息重复回放。
		pc, err := consumer.ConsumePartition(topic, p, sarama.OffsetNewest)
		if err != nil {
			log.Printf("kafka: failed to start consumer for partition %d: %v", p, err)
			continue
		}

		// 每个分区独立 goroutine，避免分区间互相阻塞。
		go func(pc sarama.PartitionConsumer) {
			defer pc.Close()
			for {
				select {
				case msg := <-pc.Messages():
					if msg == nil {
						continue
					}
					observemetrics.ObserveKafkaConsume(topic, "received")
					start := time.Now()
					if err := handler(ctx, msg); err != nil {
						observemetrics.ObserveKafkaConsume(topic, "handler_error")
						// 失败耗时同样重要：可用于定位某 topic 在错误分支是否异常变慢。
						observemetrics.ObserveKafkaHandlerDuration(topic, "handler_error", time.Since(start).Seconds())
						log.Printf("kafka: handler error for message at offset %d: %v", msg.Offset, err)
					} else {
						observemetrics.ObserveKafkaConsume(topic, "ok")
						// 成功耗时用于估算 topic 级 e2e p95。
						observemetrics.ObserveKafkaHandlerDuration(topic, "ok", time.Since(start).Seconds())
					}
				case err := <-pc.Errors():
					if err != nil {
						observemetrics.ObserveKafkaConsume(topic, "consumer_error")
						log.Printf("kafka: consumer error: %v", err)
					}
				case <-ctx.Done():
					return
				}
			}
		}(pc)
	}

	return nil
}

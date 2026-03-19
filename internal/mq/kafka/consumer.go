package kafka

import (
	"context"
	"log"

	"github.com/Shopify/sarama"
)

// MessageHandler 用于处理消费到的消息。
type MessageHandler func(ctx context.Context, msg *sarama.ConsumerMessage) error

// StartSimpleConsumer 启动一个简单的消费者，消费指定 topic 的所有分区（开发用）。
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
		pc, err := consumer.ConsumePartition(topic, p, sarama.OffsetNewest)
		if err != nil {
			log.Printf("kafka: failed to start consumer for partition %d: %v", p, err)
			continue
		}

		// 每个 partition 起一个 goroutine 处理
		go func(pc sarama.PartitionConsumer) {
			defer pc.Close()
			for {
				select {
				case msg := <-pc.Messages():
					if msg == nil {
						continue
					}
					if err := handler(ctx, msg); err != nil {
						log.Printf("kafka: handler error for message at offset %d: %v", msg.Offset, err)
					}
				case err := <-pc.Errors():
					if err != nil {
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

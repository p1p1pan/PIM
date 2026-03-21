package kafka

import (
	"context"
	"errors"
	"log"

	"github.com/Shopify/sarama"
)

// ProducerConfig 用于配置 Kafka Producer。
type ProducerConfig struct {
	Brokers []string
}

// Producer 实现了一个简单的同步 Kafka 生产者。
type Producer struct {
	producer sarama.SyncProducer
}

// NewProducer 根据配置创建 Kafka Producer。
// 注意：出错时返回 nil 且打印日志，调用方可以选择降级处理。
func NewProducer(cfg *ProducerConfig) *Producer {
	if cfg == nil || len(cfg.Brokers) == 0 {
		log.Printf("kafka: empty broker list, producer disabled")
		return nil
	}

	saramaCfg := sarama.NewConfig()
	saramaCfg.Producer.RequiredAcks = sarama.WaitForAll
	saramaCfg.Producer.Retry.Max = 3
	saramaCfg.Producer.Return.Successes = true

	p, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		log.Printf("kafka: failed to create producer: %v", err)
		return nil
	}
	log.Printf("kafka: producer connected to brokers: %v", cfg.Brokers)
	return &Producer{producer: p}
}

// SendMessage 发送一条消息到指定 topic。
func (p *Producer) SendMessage(ctx context.Context, topic, key string, value []byte) error {
	if p == nil || p.producer == nil {
		// 避免“接口成功但消息未写入 Kafka”的静默失败。
		return errors.New("kafka producer not initialized")
	}
	msg := &sarama.ProducerMessage{
		Topic: topic,
		Value: sarama.ByteEncoder(value),
	}
	if key != "" {
		msg.Key = sarama.StringEncoder(key)
	}

	partition, offset, err := p.producer.SendMessage(msg)
	if err != nil {
		return err
	}
	log.Printf("kafka: sent message to %s [partition=%d, offset=%d]", topic, partition, offset)
	return nil
}

// Close 关闭底层 producer。
func (p *Producer) Close() error {
	if p == nil || p.producer == nil {
		return nil
	}
	return p.producer.Close()
}

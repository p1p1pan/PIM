package kafka

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Shopify/sarama"

	"pim/internal/config"
	observemetrics "pim/internal/observability/metrics"
)

// ProducerConfig 用于配置 Kafka Producer。
type ProducerConfig struct {
	Brokers []string
	// LowLatency 为 true：leader 落盘即 ack、无压缩、尽快 flush（适合压测与低延迟；false 则 WaitForAll 全副本）。
	LowLatency bool
	// DebugLog 为 true 时每条成功发送打日志（默认关，避免高并发下抢 IO、拉长尾延迟）。
	DebugLog bool
}

// Producer 实现了一个简单的同步 Kafka 生产者。
type Producer struct {
	producer sarama.SyncProducer
	debugLog bool
}

type TopicMessage struct {
	Topic string
	Key   string
	Value []byte
}

// DefaultProducerConfig 使用 config 包中的 Kafka 相关环境变量（与各服务 main 中手写 Brokers 配合）。
func DefaultProducerConfig(brokers []string) *ProducerConfig {
	return &ProducerConfig{
		Brokers:    brokers,
		LowLatency: config.KafkaProducerLowLatency,
		DebugLog:   config.KafkaProducerDebugLog,
	}
}

// NewProducer 根据配置创建 Kafka Producer。
// 注意：出错时返回 nil 且打印日志，调用方可以选择降级处理。
func NewProducer(cfg *ProducerConfig) *Producer {
	if cfg == nil || len(cfg.Brokers) == 0 {
		log.Printf("kafka: empty broker list, producer disabled")
		return nil
	}

	saramaCfg := sarama.NewConfig()
	saramaCfg.Producer.Return.Successes = true
	saramaCfg.Producer.Retry.Max = 3
	saramaCfg.Producer.Compression = sarama.CompressionNone
	flushMessages := config.KafkaProducerFlushMessages
	if flushMessages <= 0 {
		flushMessages = 100
	}
	flushFreqMs := config.KafkaProducerFlushFrequencyMs
	if flushFreqMs < 0 {
		flushFreqMs = 0
	}
	flushMaxMessages := config.KafkaProducerFlushMaxMessages
	if flushMaxMessages <= 0 {
		flushMaxMessages = 1000
	}
	saramaCfg.Producer.Flush.Messages = flushMessages
	saramaCfg.Producer.Flush.Frequency = time.Duration(flushFreqMs) * time.Millisecond
	saramaCfg.Producer.Flush.MaxMessages = flushMaxMessages

	if cfg.LowLatency {
		// 低延迟模式：leader ack；但仍允许小窗口攒批，兼顾语义与吞吐。
		saramaCfg.Producer.RequiredAcks = sarama.WaitForLocal
		saramaCfg.Producer.Flush.Bytes = 0
	} else {
		saramaCfg.Producer.RequiredAcks = sarama.WaitForAll
	}

	p, err := sarama.NewSyncProducer(cfg.Brokers, saramaCfg)
	if err != nil {
		log.Printf("kafka: failed to create producer: %v", err)
		return nil
	}
	mode := "low_latency"
	if !cfg.LowLatency {
		mode = "wait_for_all"
	}
	log.Printf("kafka: producer connected brokers=%v mode=%s debug_log=%v flush_messages=%d flush_freq_ms=%d flush_max_messages=%d",
		cfg.Brokers, mode, cfg.DebugLog, flushMessages, flushFreqMs, flushMaxMessages)
	return &Producer{producer: p, debugLog: cfg.DebugLog}
}

// SendMessage 发送一条消息到指定 topic。
func (p *Producer) SendMessage(ctx context.Context, topic, key string, value []byte) error {
	_ = ctx // 预留上下文扩展位（超时/trace），当前 sarama sync producer 未直接使用。
	if p == nil || p.producer == nil {
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
		observemetrics.ObserveKafkaProduce(topic, "error")
		return err
	}
	observemetrics.ObserveKafkaProduce(topic, "ok")
	if p.debugLog {
		log.Printf("kafka: sent message to %s [partition=%d, offset=%d]", topic, partition, offset)
	}
	return nil
}

// SendMessages 批量同步发送（同一批内多条），减少与 broker 的往返次数。
func (p *Producer) SendMessages(ctx context.Context, msgs []*sarama.ProducerMessage) error {
	_ = ctx
	if p == nil || p.producer == nil {
		return errors.New("kafka producer not initialized")
	}
	if len(msgs) == 0 {
		return nil
	}
	err := p.producer.SendMessages(msgs)
	if err != nil {
		for _, m := range msgs {
			topic := ""
			if m != nil {
				topic = m.Topic
			}
			observemetrics.ObserveKafkaProduce(topic, "error")
		}
		return err
	}
	for _, m := range msgs {
		topic := ""
		if m != nil {
			topic = m.Topic
		}
		observemetrics.ObserveKafkaProduce(topic, "ok")
	}
	if p.debugLog {
		log.Printf("kafka: sent batch n=%d", len(msgs))
	}
	return nil
}

// SendTopicMessages 批量发送简化消息结构，供上层不直接依赖 sarama 类型。
func (p *Producer) SendTopicMessages(ctx context.Context, msgs []TopicMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	batch := make([]*sarama.ProducerMessage, 0, len(msgs))
	for _, m := range msgs {
		pm := &sarama.ProducerMessage{
			Topic: m.Topic,
			Value: sarama.ByteEncoder(m.Value),
		}
		if m.Key != "" {
			pm.Key = sarama.StringEncoder(m.Key)
		}
		batch = append(batch, pm)
	}
	return p.SendMessages(ctx, batch)
}

// Close 关闭底层 producer。
func (p *Producer) Close() error {
	if p == nil || p.producer == nil {
		return nil
	}
	return p.producer.Close()
}

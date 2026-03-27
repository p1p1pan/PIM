package kafka

import (
	"context"
	"log"
	"time"

	"github.com/Shopify/sarama"
	"pim/internal/config"
	observemetrics "pim/internal/kit/observability/metrics"
)

func applyHighThroughputConsumerConfig(cfg *sarama.Config) {
	if cfg == nil {
		return
	}
	buf := config.KafkaConsumerChannelBufSize
	if buf < 64 {
		buf = 256
	}
	// ChannelBufferSize 过小会在高吞吐时触发反压，先给出安全下限。
	cfg.ChannelBufferSize = buf
	maxB := config.KafkaConsumerFetchMaxBytes
	if maxB < 4096 {
		maxB = 1048576
	}
	// Fetch.Max 过小会提升 broker 往返次数，吞吐和 CPU 都会受影响。
	cfg.Consumer.Fetch.Max = int32(maxB)
	waitMs := config.KafkaConsumerMaxWaitTimeMs
	if waitMs < 1 {
		waitMs = 100
	}
	// MaxWaitTime 决定 broker 端聚合等待，值越小延迟更低但吞吐通常更差。
	cfg.Consumer.MaxWaitTime = time.Duration(waitMs) * time.Millisecond
}

func commitEveryBatch() int {
	n := config.KafkaConsumerCommitEveryBatch
	if n < 1 {
		return 1
	}
	return n
}

// MessageHandler 用于处理消费到的消息。
type MessageHandler func(ctx context.Context, msg *sarama.ConsumerMessage) error
type BatchMessageHandler func(ctx context.Context, msgs []*sarama.ConsumerMessage) error

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

// StartConsumerGroup 启动一个 consumer group。
// 语义：分区内串行处理（禁止在 ConsumeClaim 中异步起 goroutine 处理消息）。
// 提交策略：由本函数在 handler 成功后 mark + commit，实现“先处理成功后提交位移”。
func StartConsumerGroup(ctx context.Context, brokers []string, groupID string, topics []string, handler MessageHandler) error {
	if len(topics) == 0 {
		return nil
	}
	cfg := sarama.NewConfig()
	cfg.Consumer.Return.Errors = true
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cfg.Consumer.Offsets.AutoCommit.Enable = false
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRange
	applyHighThroughputConsumerConfig(cfg)

	group, err := sarama.NewConsumerGroup(brokers, groupID, cfg)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := group.Close(); err != nil {
			log.Printf("kafka consumer group close error: %v", err)
		}
	}()
	go func() {
		for err := range group.Errors() {
			if err != nil {
				log.Printf("kafka: consumer group error: %v", err)
			}
		}
	}()

	log.Printf("kafka: consuming topics=%v with group=%s", topics, groupID)
	cgh := &consumerGroupHandler{ctx: ctx, handler: handler}
	// group.Consume 在 rebalance 后会返回并再次进入循环，这属于预期行为。
	for ctx.Err() == nil {
		if err := group.Consume(ctx, topics, cgh); err != nil {
			log.Printf("kafka: consumer group consume failed group=%s topics=%v err=%v", groupID, topics, err)
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

// StartConsumerGroupBatch 与 StartConsumerGroup 类似，但按分区内小批量触发 handler。
// 仍保持分区内串行处理：一个批次完成后才会继续下一批。
func StartConsumerGroupBatch(ctx context.Context, brokers []string, groupID string, topics []string, batchSize int, batchWait time.Duration, handler BatchMessageHandler) error {
	if len(topics) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	if batchWait <= 0 {
		batchWait = 5 * time.Millisecond
	}

	cfg := sarama.NewConfig()
	cfg.Consumer.Return.Errors = true
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cfg.Consumer.Offsets.AutoCommit.Enable = false
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRange
	applyHighThroughputConsumerConfig(cfg)

	group, err := sarama.NewConsumerGroup(brokers, groupID, cfg)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := group.Close(); err != nil {
			log.Printf("kafka consumer group close error: %v", err)
		}
	}()
	go func() {
		for err := range group.Errors() {
			if err != nil {
				log.Printf("kafka: consumer group error: %v", err)
			}
		}
	}()

	log.Printf("kafka: consuming topics=%v with group=%s (batch size=%d wait=%v)", topics, groupID, batchSize, batchWait)
	cgh := &batchConsumerGroupHandler{
		ctx:       ctx,
		handler:   handler,
		batchSize: batchSize,
		batchWait: batchWait,
	}
	for ctx.Err() == nil {
		if err := group.Consume(ctx, topics, cgh); err != nil {
			log.Printf("kafka: consumer group batch consume failed group=%s topics=%v err=%v", groupID, topics, err)
			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

type consumerGroupHandler struct {
	ctx     context.Context
	handler MessageHandler
}

func (h *consumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

// ConsumeClaim 处理单条消费模型：
// - 分区内串行处理，handler 成功后才 MarkMessage；
// - commitEveryBatch 控制提交频率：值越大吞吐越好，但异常退出时重复消费窗口越大（at-least-once）。
func (h *consumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	topic := claim.Topic()
	every := commitEveryBatch()
	pending := 0
	commitPending := func() {
		if pending > 0 {
			session.Commit()
			pending = 0
		}
	}
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if msg == nil {
				continue
			}
			observemetrics.ObserveKafkaConsume(topic, "received")
			start := time.Now()
			if err := h.handler(h.ctx, msg); err != nil {
				observemetrics.ObserveKafkaConsume(topic, "handler_error")
				observemetrics.ObserveKafkaHandlerDuration(topic, "handler_error", time.Since(start).Seconds())
				log.Printf("kafka: consumer group handler error topic=%s partition=%d offset=%d err=%v", msg.Topic, msg.Partition, msg.Offset, err)
				continue
			}
			observemetrics.ObserveKafkaConsume(topic, "ok")
			observemetrics.ObserveKafkaHandlerDuration(topic, "ok", time.Since(start).Seconds())
			// 仅成功后提交位移，保证“先处理、后提交”；重复消费由上层幂等兜底。
			session.MarkMessage(msg, "")
			pending++
			if pending >= every {
				commitPending()
			}
		case <-session.Context().Done():
			commitPending()
			return nil
		case <-h.ctx.Done():
			commitPending()
			return nil
		}
	}
}

type batchConsumerGroupHandler struct {
	ctx       context.Context
	handler   BatchMessageHandler
	batchSize int
	batchWait time.Duration
}

func (h *batchConsumerGroupHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (h *batchConsumerGroupHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

// ConsumeClaim 处理分区内小批量消费：
// - 以 batchSize 或 batchWait 触发 flush；
// - 仅当整批 handler 成功后才 MarkMessage；
// - 提交策略仍是 at-least-once，重复窗口由 commitEveryBatch 决定。
func (h *batchConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	topic := claim.Topic()
	every := commitEveryBatch()
	pending := 0
	timer := time.NewTimer(h.batchWait)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	commitPending := func() {
		if pending > 0 {
			session.Commit()
			pending = 0
		}
	}
	flush := func(batch []*sarama.ConsumerMessage) error {
		if len(batch) == 0 {
			return nil
		}
		start := time.Now()
		if err := h.handler(h.ctx, batch); err != nil {
			observemetrics.ObserveKafkaConsume(topic, "handler_error")
			observemetrics.ObserveKafkaHandlerDuration(topic, "handler_error", time.Since(start).Seconds())
			return err
		}
		observemetrics.ObserveKafkaConsume(topic, "ok")
		observemetrics.ObserveKafkaHandlerDuration(topic, "ok", time.Since(start).Seconds())
		for _, m := range batch {
			session.MarkMessage(m, "")
		}
		pending++
		if pending >= every {
			commitPending()
		}
		return nil
	}

	batch := make([]*sarama.ConsumerMessage, 0, h.batchSize)
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				if err := flush(batch); err != nil {
					return err
				}
				commitPending()
				return nil
			}
			if msg == nil {
				continue
			}
			observemetrics.ObserveKafkaConsume(topic, "received")
			batch = append(batch, msg)
			if len(batch) == 1 {
				timer.Reset(h.batchWait)
			}
			if len(batch) >= h.batchSize {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if err := flush(batch); err != nil {
					log.Printf("kafka: consumer group batch handler error topic=%s partition=%d offset=%d err=%v", msg.Topic, msg.Partition, msg.Offset, err)
					return err
				}
				batch = batch[:0]
			}
		case <-timer.C:
			if len(batch) > 0 {
				last := batch[len(batch)-1]
				if err := flush(batch); err != nil {
					log.Printf("kafka: consumer group batch handler error topic=%s partition=%d offset=%d err=%v", last.Topic, last.Partition, last.Offset, err)
					return err
				}
				batch = batch[:0]
			}
		case <-session.Context().Done():
			if err := flush(batch); err != nil {
				return err
			}
			commitPending()
			return nil
		case <-h.ctx.Done():
			if err := flush(batch); err != nil {
				return err
			}
			commitPending()
			return nil
		}
	}
}

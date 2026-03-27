package mq

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	"pim/internal/conversation/model"
	logmodel "pim/internal/log/model"
	"pim/internal/kit/mq/kafka"

	conversationservice "pim/internal/conversation/service"
)

// handleImMessage 兼容单条入口，内部复用批处理逻辑。
func handleImMessage(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	return handleImMessageBatch(ctx, svc, rdb, producer, []*sarama.ConsumerMessage{msg})
}

// handleImMessageBatch 小批量处理单聊消息：
// 1) 批量幂等落库（upsert）；2) 仅对新创建消息执行未读+推送。
func handleImMessageBatch(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, producer *kafka.Producer, msgs []*sarama.ConsumerMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	kms := make([]model.KafkaMessage, 0, len(msgs))
	inputs := make([]conversationservice.SendInput, 0, len(msgs))
	for _, msg := range msgs {
		km, err := model.DecodeKafkaMessage(msg.Value)
		if err != nil {
			emitConsumerLog(producer, logmodel.Log{
				TS:        time.Now(),
				Level:     "error",
				Service:   "conversation",
				TraceID:   "unknown",
				Msg:       "im-message decode failed",
				ErrorCode: "decode_failed",
			})
			return err
		}
		if km.TraceID == "" {
			km.TraceID = "unknown"
		}
		if km.ClientMsgID == "" {
			km.ClientMsgID = uuid.NewString()
		}
		kms = append(kms, km)
		inputs = append(inputs, conversationservice.SendInput{
			FromID:      km.FromUserID,
			ToID:        km.ToUserID,
			Content:     km.Content,
			ClientMsgID: km.ClientMsgID,
		})
	}
	saved, created, err := svc.SendMessageIdempotentBatch(inputs)
	if err != nil {
		km := kms[0]
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "conversation",
			TraceID:   km.TraceID,
			Msg:       "im-message persist failed",
			EventID:   km.ClientMsgID,
			UserID:    uint64(km.FromUserID),
			ErrorCode: "persist_failed",
		})
		return err
	}

	var pushBatch []model.KafkaMessage
	for i, km := range kms {
		if saved[i] == nil {
			continue
		}
		if created[i] {
			pushBatch = append(pushBatch, km)
		}
		if !config.KafkaConversationConsumerVerboseLog {
			continue
		}
		log.Printf("[trace=%s] im-message handled msgID=%d created=%v", km.TraceID, saved[i].ID, created[i])
		emitConsumerLog(producer, logmodel.Log{
			Level:          "info",
			Service:        "conversation",
			TraceID:        km.TraceID,
			Msg:            "im-message consumed",
			EventID:        km.ClientMsgID,
			UserID:         uint64(km.FromUserID),
			ConversationID: fmt.Sprintf("%d:%d", minUint(km.FromUserID, km.ToUserID), maxUint(km.FromUserID, km.ToUserID)),
		})
	}
	if len(pushBatch) > 0 {
		applyRealtimeSideEffectsBatch(ctx, rdb, producer, pushBatch)
	}
	return nil
}

// applyRealtimeSideEffectsBatch 未读计数与 push topic 使用 Pipeline + 批量 SendMessages，降低高 QPS 下 Redis/Kafka 往返。
func applyRealtimeSideEffectsBatch(ctx context.Context, rdb *redis.Client, producer *kafka.Producer, kms []model.KafkaMessage) {
	if len(kms) == 0 {
		return
	}
	if rdb != nil {
		pr := rdb.Pipeline()
		for i := range kms {
			km := &kms[i]
			userA, userB := km.FromUserID, km.ToUserID
			if userA > userB {
				userA, userB = userB, userA
			}
			convKey := fmt.Sprintf("%d:%d", userA, userB)
			unreadKey := fmt.Sprintf("msg:unread:%d:%s", km.ToUserID, convKey)
			_ = pr.Incr(ctx, unreadKey)
		}
		_, _ = pr.Exec(ctx)
	}
	if producer == nil {
		return
	}
	msgs := make([]*sarama.ProducerMessage, 0, len(kms))
	for i := range kms {
		km := &kms[i]
		userA, userB := km.FromUserID, km.ToUserID
		if userA > userB {
			userA, userB = userB, userA
		}
		convKey := fmt.Sprintf("%d:%d", userA, userB)
		data, err := model.EncodeKafkaMessagePB(*km)
		if err != nil {
			continue
		}
		msgs = append(msgs, &sarama.ProducerMessage{
			Topic: "im-message-push",
			Key:   sarama.StringEncoder(convKey),
			Value: sarama.ByteEncoder(data),
		})
	}
	if len(msgs) == 0 {
		return
	}
	if err := producer.SendMessages(ctx, msgs); err != nil {
		km0 := kms[0]
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "warn",
			Service:   "conversation",
			TraceID:   km0.TraceID,
			Msg:       "im-message publish push event batch failed",
			EventID:   km0.ClientMsgID,
			UserID:    uint64(km0.FromUserID),
			ErrorCode: "push_event_publish_failed",
		})
	}
}

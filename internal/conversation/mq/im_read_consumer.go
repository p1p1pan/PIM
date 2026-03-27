package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Shopify/sarama"
	"github.com/redis/go-redis/v9"

	"pim/internal/conversation/model"
	logmodel "pim/internal/log/model"
	"pim/internal/kit/mq/kafka"

	conversationservice "pim/internal/conversation/service"
)

// handleImReadMessage 处理已读事件：
// 1) 推进会话读游标；2) 清理该会话未读计数键。
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
		// 脏数据直接丢弃，避免污染读游标。
		return nil
	}
	traceID := rm.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	eventID := rm.ConversationID
	_, err := svc.HandleReadEvent(rm.UserID, rm.ConversationID)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:             time.Now(),
			Level:          "error",
			Service:        "conversation",
			TraceID:        traceID,
			Msg:            "im-message-read handle failed",
			EventID:        eventID,
			UserID:         uint64(rm.UserID),
			ConversationID: rm.ConversationID,
			ErrorCode:      "read_handle_failed",
		})
		return err
	}
	if rdb != nil {
		// 已读事件成功后删除未读键，保持 Redis 与读游标一致。
		unreadKey := fmt.Sprintf("msg:unread:%d:%s", rm.UserID, rm.ConversationID)
		_ = rdb.Del(ctx, unreadKey).Err()
	}
	emitConsumerLog(producer, logmodel.Log{
		Level:          "info",
		Service:        "conversation",
		TraceID:        traceID,
		Msg:            "im-message-read consumed",
		EventID:        eventID,
		UserID:         uint64(rm.UserID),
		ConversationID: rm.ConversationID,
	})
	return nil
}

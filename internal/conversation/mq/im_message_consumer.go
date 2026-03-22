package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Shopify/sarama"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"pim/internal/conversation/model"
	pbgateway "pim/internal/gateway/pb"
	logmodel "pim/internal/log/model"
	"pim/internal/mq/kafka"

	conversationservice "pim/internal/conversation/service"
)

// handleImMessage 处理单聊消息事件：
// 1) 幂等落库；2) 目标会话未读 +1；3) 目标用户在本节点在线时做实时推送。
func handleImMessage(ctx context.Context, svc *conversationservice.Service, rdb *redis.Client, pushClient pbgateway.PushServiceClient, producer *kafka.Producer, msg *sarama.ConsumerMessage) error {
	var km model.KafkaMessage
	if err := json.Unmarshal(msg.Value, &km); err != nil {
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
	traceID := km.TraceID
	if traceID == "" {
		traceID = "unknown"
	}
	eventID := km.ClientMsgID
	if eventID == "" {
		// Defensive fallback for non-standard producers: keep event tracing non-empty.
		eventID = uuid.NewString()
	}
	saved, _, err := svc.SendMessageIdempotent(km.FromUserID, km.ToUserID, km.Content, eventID)
	if err != nil {
		emitConsumerLog(producer, logmodel.Log{
			TS:        time.Now(),
			Level:     "error",
			Service:   "conversation",
			TraceID:   traceID,
			Msg:       "im-message persist failed",
			EventID:   eventID,
			UserID:    uint64(km.FromUserID),
			ErrorCode: "persist_failed",
		})
		return err
	}
	if rdb != nil {
		// conversation key 统一按 userID 小->大拼接，避免同一会话出现两套 key。
		userA, userB := km.FromUserID, km.ToUserID
		if userA > userB {
			userA, userB = userB, userA
		}
		convKey := fmt.Sprintf("%d:%d", userA, userB)
		unreadKey := fmt.Sprintf("msg:unread:%d:%s", km.ToUserID, convKey)
		_ = rdb.Incr(ctx, unreadKey).Err()

		// 读取目标用户连接归属，用于判断是否由当前 gateway 节点推送。
		key := fmt.Sprintf("ws:conn:%d", km.ToUserID)
		val, err := rdb.Get(ctx, key).Result()
		if err == nil && val != "" {
			// 当前约定只对本节点连接执行 PushToConn，避免重复推送。
			if !strings.HasPrefix(val, "gateway-1:") {
				return nil
			}
			if pushClient != nil {
				// 推送失败只记告警，不影响消息持久化成功语义。
				_, pushErr := pushClient.PushToConn(ctx, &pbgateway.PushToConnRequest{
					FromUserId: uint64(km.FromUserID),
					ToUserId:   uint64(km.ToUserID),
					Content:    km.Content,
				})
				if pushErr != nil {
					emitConsumerLog(producer, logmodel.Log{
						TS:        time.Now(),
						Level:     "warn",
						Service:   "conversation",
						TraceID:   traceID,
						Msg:       "im-message push failed",
						EventID:   eventID,
						UserID:    uint64(km.FromUserID),
						ErrorCode: "push_failed",
					})
				}
			}
		}
	}
	log.Printf("[trace=%s] im-message handled msgID=%d", traceID, saved.ID)
	emitConsumerLog(producer, logmodel.Log{
		Level:          "info",
		Service:        "conversation",
		TraceID:        traceID,
		Msg:            "im-message consumed",
		EventID:        eventID,
		UserID:         uint64(km.FromUserID),
		ConversationID: fmt.Sprintf("%d:%d", minUint(km.FromUserID, km.ToUserID), maxUint(km.FromUserID, km.ToUserID)),
	})
	return nil
}

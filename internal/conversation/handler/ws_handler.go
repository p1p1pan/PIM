package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"pim/internal/conversation/model"
	pbconversation "pim/internal/conversation/pb"
	"pim/internal/kit/mq/kafka"
	observemetrics "pim/internal/kit/observability/metrics"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// MessageProducer 封装 Kafka 生产能力，便于在 WebSocket 入口复用。
// SendMessageWithHeaders 用于透传 ingress_ts_ns 做 e2e 测量；
// 旧实现只有 SendMessage 时 WebSocketHandler 会优雅降级，仅跳过 e2e header。
type MessageProducer interface {
	SendMessage(ctx context.Context, topic, key string, value []byte) error
	SendMessageWithHeaders(ctx context.Context, topic, key string, value []byte, headers map[string][]byte) error
}

// WebSocketHandler 负责接入 WebSocket 并把上行消息写入 Kafka。
func WebSocketHandler(client pbconversation.ConversationServiceClient, producer MessageProducer) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade to WebSocket: %v", err)
			return
		}
		observemetrics.ObserveGatewayWSConnected()
		defer observemetrics.ObserveGatewayWSDisconnected()
		setUserConn(userID, conn)
		for {
			var msg model.WSIncomingMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
			}
			// t0 = 收到 WS 帧的时刻；既作为 ack 计时起点，也写入 Kafka header 做 e2e 起点。
			t0 := time.Now()
			if msg.ClientMsgID == "" {
				// Ensure every message has a stable event_id for cross-service tracing.
				msg.ClientMsgID = uuid.NewString()
			}
			tid, _ := c.Get("trace_id")
			if producer != nil {
				kmsg := model.KafkaMessage{
					TraceID:     tid.(string),
					FromUserID:  userID,
					ToUserID:    msg.To,
					Content:     msg.Content,
					ClientMsgID: msg.ClientMsgID,
				}
				result := "ok"
				if data, err := model.EncodeKafkaMessagePB(kmsg); err != nil {
					log.Printf("ws protobuf marshal kafka message: %v", err)
					observemetrics.ObserveGatewayWSSendDuration("im", "error", time.Since(t0).Seconds())
					continue
				} else {
					headers := map[string][]byte{
						kafka.HeaderIngressTsNs: kafka.EncodeIngressTsNs(t0.UnixNano()),
					}
					if err := producer.SendMessageWithHeaders(context.Background(), "im-message", conversationKey(userID, msg.To), data, headers); err != nil {
						log.Printf("ws kafka send: %v", err)
						_ = writeJSONToUser(userID, model.WSServerAck{Type: "ack_error", ClientMsgID: msg.ClientMsgID, Error: err.Error()})
						result = "error"
					} else {
						// 入队成功即确认，便于客户端测量「发送路径」而不等待对端 Push（端到端见 bench-msg-latency -measure e2e）。
						_ = writeJSONToUser(userID, model.WSServerAck{Type: "ack", ClientMsgID: msg.ClientMsgID})
					}
				}
				// 对齐 bench.ack：从入口到 ack 写出的全部耗时都落 histogram。
				observemetrics.ObserveGatewayWSSendDuration("im", result, time.Since(t0).Seconds())
			}
		}
		deleteUserConn(userID)
		_ = conn.Close()
	}
}

// conversationKey 把同一对单聊用户稳定映射到同一个 Kafka key，保证会话内消息落同分区以维持顺序。
func conversationKey(a, b uint) string {
	if a < b {
		return fmt.Sprintf("%d:%d", a, b)
	}
	return fmt.Sprintf("%d:%d", b, a)
}

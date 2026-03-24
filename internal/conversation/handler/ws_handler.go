package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"pim/internal/conversation/model"
	pbconversation "pim/internal/conversation/pb"
	observemetrics "pim/internal/observability/metrics"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// MessageProducer 封装 Kafka 生产能力，便于在 WebSocket 入口复用。
type MessageProducer interface {
	SendMessage(ctx context.Context, topic, key string, value []byte) error
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
				if data, err := model.EncodeKafkaMessagePB(kmsg); err != nil {
					log.Printf("ws protobuf marshal kafka message: %v", err)
					continue
				} else if err := producer.SendMessage(context.Background(), "im-message", conversationKey(userID, msg.To), data); err != nil {
					log.Printf("ws kafka send: %v", err)
					_ = writeJSONToUser(userID, model.WSServerAck{Type: "ack_error", ClientMsgID: msg.ClientMsgID, Error: err.Error()})
				} else {
					// 入队成功即确认，便于客户端测量「发送路径」而不等待对端 Push（端到端见 bench-msg-latency -measure e2e）。
					_ = writeJSONToUser(userID, model.WSServerAck{Type: "ack", ClientMsgID: msg.ClientMsgID})
				}
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

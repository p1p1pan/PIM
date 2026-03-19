package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"pim/internal/conversation/model"
	pbconversation "pim/internal/conversation/pb"
)

// WebSocket 连接管理：维护 userID -> websocket.Conn 的本地映射。
var (
	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsConns  = make(map[uint]*websocket.Conn)
	wsMu     sync.RWMutex
)

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
		wsMu.Lock()
		wsConns[userID] = conn
		wsMu.Unlock()
		for {
			var msg model.WSIncomingMessage
			if err := conn.ReadJSON(&msg); err != nil {
				break
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
				if data, err := json.Marshal(kmsg); err == nil {
					_ = producer.SendMessage(context.Background(), "im-message", "", data)
				}
			}
		}
		wsMu.Lock()
		delete(wsConns, userID)
		wsMu.Unlock()
		_ = conn.Close()
	}
}

// PushToUser 按 userID 查找本地连接并下行推送消息。
func PushToUser(toUserID uint, fromUserID uint, content string) error {
	wsMu.RLock()
	conn := wsConns[toUserID]
	wsMu.RUnlock()
	if conn == nil {
		return fmt.Errorf("user %d not connected", toUserID)
	}
	if err := conn.WriteJSON(model.PushMessage{From: fromUserID, Content: content}); err != nil {
		return fmt.Errorf("write to user %d failed: %w", toUserID, err)
	}
	return nil
}

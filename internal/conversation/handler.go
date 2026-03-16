package conversation

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbconversation "pim/internal/conversation/pb"
)

// WebSocket 升级与连接表：upgrader 将 HTTP 升级为 WebSocket；wsConnections 按 userID 存当前在线连接，供推送用。
var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true // 开发阶段放行，生产环境应按 Origin 白名单校验
		},
	}
	wsConnections = make(map[uint]*websocket.Conn)
	wsMu          sync.RWMutex
)

// RegisterRoutes 已移除：GET /messages 现由 Gateway 直接调 conversation gRPC 处理。

// WebSocketHandler 供 Gateway 挂在 GET /ws 上（需先挂 AuthMiddleware）。流程：鉴权后取 userID → Upgrade 为 WebSocket →
// 将 conn 存入 wsConnections[userID] → 循环 ReadJSON 收客户端消息，校验好友、落库、若对方在线则从 wsConnections 取 conn 推送。
func WebSocketHandler(client pbconversation.ConversationServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		// 升级为 WebSocket
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			log.Printf("Failed to upgrade to WebSocket: %v", err)
			return
		}
		// 将连接存入 wsConnections
		wsMu.Lock()
		wsConnections[userID] = conn
		wsMu.Unlock()
		log.Printf("User %d connected to WebSocket", userID)
		// 循环读取客户端消息
		for {
			var msg struct {
				To      uint   `json:"to"`
				Content string `json:"content"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				log.Printf("Failed to read JSON: %v", err)
				break
			}
			// 调用 gRPC 发送消息
			_, err := client.SendMessage(context.Background(), &pbconversation.SendMessageRequest{
				FromUserId: uint64(userID),
				ToUserId:   uint64(msg.To),
				Content:    msg.Content,
			})
			// 处理错误
			if err != nil {
				if st, ok := status.FromError(err); ok && st.Code() == codes.PermissionDenied {
					_ = conn.WriteJSON(gin.H{
						"error": "not friends, cannot send message",
						"to":    msg.To,
					})
					continue
				}
				log.Printf("Failed to send message via gRPC: %v", err)
				continue
			}
			// 从 wsConnections 取连接并发送消息
			wsMu.RLock()
			toConn := wsConnections[msg.To]
			wsMu.RUnlock()
			if toConn != nil {
				out := struct {
					From    uint   `json:"from"`
					Content string `json:"content"`
				}{
					From:    userID,
					Content: msg.Content,
				}
				if err := toConn.WriteJSON(out); err != nil {
					log.Printf("Failed to send message: %v", err)
					break
				}
			}
			log.Printf("Message sent to user %d", msg.To)
		}

		// 删除连接
		wsMu.Lock()
		delete(wsConnections, userID)
		wsMu.Unlock()
		conn.Close()
	}
}

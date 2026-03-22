package handler

import (
	"fmt"
	"sync"

	"github.com/gorilla/websocket"

	"pim/internal/conversation/model"
)

// wsConns 维护当前 gateway 进程内的 userID -> conn 映射。
// 注意：这是单进程内存态注册表，多 gateway 部署时只表示“本节点在线”。
var (
	wsConns = make(map[uint]*websocket.Conn)
	wsMu    sync.RWMutex
)

func setUserConn(userID uint, conn *websocket.Conn) {
	wsMu.Lock()
	wsConns[userID] = conn
	wsMu.Unlock()
}

func deleteUserConn(userID uint) {
	wsMu.Lock()
	delete(wsConns, userID)
	wsMu.Unlock()
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

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
	wsConns = make(map[uint]*wsConnState)
	wsMu    sync.RWMutex
)

type wsConnState struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func setUserConn(userID uint, conn *websocket.Conn) {
	wsMu.Lock()
	wsConns[userID] = &wsConnState{conn: conn}
	wsMu.Unlock()
}

func deleteUserConn(userID uint) {
	wsMu.Lock()
	delete(wsConns, userID)
	wsMu.Unlock()
}

// PushToUser 按 userID 查找本地连接并下行推送消息。
func PushToUser(toUserID uint, fromUserID uint, content string) error {
	if err := writeJSONToUser(toUserID, model.PushMessage{From: fromUserID, Content: content}); err != nil {
		return fmt.Errorf("write to user %d failed: %w", toUserID, err)
	}
	return nil
}

func writeJSONToUser(userID uint, payload interface{}) error {
	wsMu.RLock()
	state := wsConns[userID]
	wsMu.RUnlock()
	if state == nil || state.conn == nil {
		return fmt.Errorf("user %d not connected", userID)
	}
	state.writeMu.Lock()
	defer state.writeMu.Unlock()
	return state.conn.WriteJSON(payload)
}

// WriteBytesToUser 将已编码的 JSON 字节直接写入对应用户的 WebSocket（TextMessage），
// 用于批内同 payload 复用，避免重复 marshal。
func WriteBytesToUser(userID uint, jsonBytes []byte) error {
	wsMu.RLock()
	state := wsConns[userID]
	wsMu.RUnlock()
	if state == nil || state.conn == nil {
		return fmt.Errorf("user %d not connected", userID)
	}
	state.writeMu.Lock()
	defer state.writeMu.Unlock()
	return state.conn.WriteMessage(websocket.TextMessage, jsonBytes)
}

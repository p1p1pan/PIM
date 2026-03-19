package model

// LoginRequest 是 Gateway 登录接口请求体。
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// RegisterRequest 是 Gateway 注册接口请求体。
type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AddFriendRequest 是 Gateway 添加好友接口请求体。
type AddFriendRequest struct {
	FriendID uint `json:"friend_id"`
}

// ConversationReadEvent 是写入 Kafka 的会话已读事件结构。
type ConversationReadEvent struct {
	TraceID        string `json:"trace_id"`
	UserID         uint   `json:"user_id"`
	PeerID         uint   `json:"peer_id"`
	ConversationID string `json:"conversation_id"`
}

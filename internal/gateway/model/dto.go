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

// SendFriendRequestRequest 是发送好友申请请求体。
type SendFriendRequestRequest struct {
	ToUserID uint   `json:"to_user_id"`
	Remark   string `json:"remark"`
}

// ListFriendRequestsQuery 是好友申请列表查询参数。
type ListFriendRequestsQuery struct {
	Status string `form:"status"` // pending/accepted/rejected/cancelled，空表示全部
	Cursor uint64 `form:"cursor"` // 基于 request_id 的游标
	Limit  uint32 `form:"limit"`  // 默认20，最大50
}

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

// CreateGroupRequest 是创建群请求体。
type CreateGroupRequest struct {
	Name          string   `json:"name"`
	MemberUserIDs []uint64 `json:"member_user_ids"`
}

// AddGroupMemberRequest 是添加群成员请求体。
type AddGroupMemberRequest struct {
	TargetUserID uint64 `json:"target_user_id"`
}

// SendGroupMessageRequest 是发送群消息请求体。
type SendGroupMessageRequest struct {
	Content     string `json:"content"`
	ClientMsgID string `json:"client_msg_id"`
}

// UpdateGroupRequest 是更新群资料请求体。
type UpdateGroupRequest struct {
	Name   string `json:"name"`
	Notice string `json:"notice"`
}

// TransferGroupOwnerRequest 是群主转让请求体。
type TransferGroupOwnerRequest struct {
	TargetUserID uint64 `json:"target_user_id"`
}

// FilePrepareRequest 是文件上传申请请求体。
type FilePrepareRequest struct {
	ClientUploadID string `json:"client_upload_id"`
	FileName       string `json:"file_name"`
	FileSize       int64  `json:"file_size"`
	MimeType       string `json:"mime_type"`
	BizType        string `json:"biz_type"` // private/group
	PeerID         uint64 `json:"peer_id"`
	GroupID        uint64 `json:"group_id"`
}

// FileCommitRequest 是文件上传确认请求体。
type FileCommitRequest struct {
	ETag string `json:"etag"`
}

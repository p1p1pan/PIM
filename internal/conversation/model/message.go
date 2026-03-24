package model

import "time"

// Message 表示单聊消息实体，包含幂等键与会话内顺序号。
type Message struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FromUserID  uint      `gorm:"not null" json:"from_user_id"`
	ClientMsgID string    `gorm:"type:varchar(64);not null;index:idx_sender_clientmsg,unique" json:"client_msg_id"`
	Seq         uint      `gorm:"not null;default:0" json:"seq"`
	ToUserID    uint      `gorm:"not null" json:"to_user_id"`
	Content     string    `gorm:"type:text;not null" json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

// MessageRead 表示用户在会话内的已读游标。
type MessageRead struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ConversationID uint      `gorm:"not null;index:idx_conv_user_read,unique" json:"conversation_id"`
	UserID         uint      `gorm:"not null;index:idx_conv_user_read,unique" json:"user_id"`
	LastReadSeq    uint      `gorm:"not null;default:0" json:"last_read_seq"`
	UpdatedAt      time.Time `json:"updated_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// KafkaMessage 是 WebSocket 上行写入 Kafka 的消息事件。
type KafkaMessage struct {
	TraceID     string `json:"trace_id"`
	FromUserID  uint   `json:"from_user_id"`
	ToUserID    uint   `json:"to_user_id"`
	Content     string `json:"content"`
	ClientMsgID string `json:"client_msg_id"`
}

// ImReadKafkaMessage 是会话已读事件的 Kafka 载荷。
type ImReadKafkaMessage struct {
	TraceID        string `json:"trace_id"`
	UserID         uint   `json:"user_id"`
	PeerID         uint   `json:"peer_id"`
	ConversationID string `json:"conversation_id"`
}

// PushMessage 是 Gateway 下行推送给 WebSocket 客户端的消息体。
type PushMessage struct {
	From    uint   `json:"from"`
	Content string `json:"content"`
}

// WSIncomingMessage 是 WebSocket 上行发送给 Gateway 的消息体。
type WSIncomingMessage struct {
	To          uint   `json:"to"`
	Content     string `json:"content"`
	ClientMsgID string `json:"client_msg_id"`
}

// WSServerAck 是网关对单聊上行的确认（Kafka 入队成功后立即下发；无 from/content，Web 端会忽略）。
type WSServerAck struct {
	Type        string `json:"type"` // "ack" | "ack_error"
	ClientMsgID string `json:"client_msg_id"`
	Error       string `json:"error,omitempty"`
}

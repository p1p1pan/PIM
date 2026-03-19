package model

import "time"

// Conversation 表示两人会话的聚合状态（最后消息、最后 seq、时间戳）。
type Conversation struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserA         uint      `gorm:"not null;index:idx_conv_users,unique" json:"user_a"`
	UserB         uint      `gorm:"not null;index:idx_conv_users,unique" json:"user_b"`
	LastMessageID uint      `json:"last_message_id"`
	LastSeq       uint      `json:"last_seq"`
	LastMessageAt time.Time `json:"last_message_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

package model

import "time"

// Group 表示一个群聊会话。
type Group struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"type:varchar(128);not null" json:"name"`
	OwnerUserID uint      `gorm:"not null;index" json:"owner_user_id"`
	Notice      string    `gorm:"type:text;not null;default:''" json:"notice"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GroupMember 表示群成员关系。
type GroupMember struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	GroupID   uint      `gorm:"not null;index:idx_group_user,unique;index:idx_group_role" json:"group_id"`
	UserID    uint      `gorm:"not null;index:idx_group_user,unique" json:"user_id"`
	Role      string    `gorm:"type:varchar(16);not null;default:member;index:idx_group_role" json:"role"` // owner/admin/member
	CreatedAt time.Time `json:"created_at"`
}

// GroupMessage 表示群消息落库实体。
type GroupMessage struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	GroupID     uint      `gorm:"not null;uniqueIndex:idx_group_seq" json:"group_id"`
	FromUserID  uint      `gorm:"not null;index" json:"from_user_id"`
	MessageType string    `gorm:"type:varchar(16);not null;default:text" json:"message_type"` // text/system
	Content     string    `gorm:"type:text;not null" json:"content"`
	Seq         uint64    `gorm:"not null;default:0;uniqueIndex:idx_group_seq" json:"seq"`
	EventID     string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_group_event_id" json:"event_id"`
	CreatedAt   time.Time `json:"created_at"`
}

// GroupKafkaMessage 是 group-message topic 的事件结构。
type GroupKafkaMessage struct {
	TraceID string `json:"trace_id"`
	EventID string `json:"event_id"`
	GroupID uint   `json:"group_id"`
	From    uint   `json:"from"`
	Content string `json:"content"`
}

// GroupPushMessage 是下行给在线成员的群消息事件。
type GroupPushMessage struct {
	Type        string `json:"type"` // group_message
	GroupID     uint   `json:"group_id"`
	FromUserID  uint   `json:"from_user_id"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"`
	Seq         uint64 `json:"seq"`
}

// GroupReadState 记录用户在群内的已读游标。
type GroupReadState struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	GroupID uint   `gorm:"not null;uniqueIndex:idx_group_read_user" json:"group_id"`
	UserID  uint   `gorm:"not null;uniqueIndex:idx_group_read_user" json:"user_id"`
	ReadSeq uint64 `gorm:"not null;default:0" json:"read_seq"`
}

package model

import "time"

// Friend 表示双向好友关系中的一条边。
type Friend struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index:idx_user_friend,unique;not null" json:"user_id"`
	FriendID  uint      `gorm:"index:idx_user_friend,unique;not null" json:"friend_id"`
	CreatedAt time.Time `json:"created_at"`
}

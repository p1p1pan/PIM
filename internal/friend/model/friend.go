package model

import "time"

// Friend 表示双向好友关系中的一条边。
type Friend struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index:idx_user_friend,unique;not null" json:"user_id"`
	FriendID  uint      `gorm:"index:idx_user_friend,unique;not null" json:"friend_id"`
	CreatedAt time.Time `json:"created_at"`
}

// FriendRequest 表示好友申请记录。
type FriendRequest struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	FromUserID uint      `gorm:"not null;index:idx_friend_req_pair_status" json:"from_user_id"`
	ToUserID   uint      `gorm:"not null;index:idx_friend_req_pair_status" json:"to_user_id"`
	Status     string    `gorm:"type:varchar(16);not null;default:pending;index:idx_friend_req_pair_status" json:"status"` // pending/accepted/rejected
	Remark     string    `gorm:"type:varchar(255)" json:"remark"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Blacklist 表示拉黑关系。
type Blacklist struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	UserID        uint      `gorm:"not null;index:idx_black_pair,unique" json:"user_id"`
	BlockedUserID uint      `gorm:"not null;index:idx_black_pair,unique" json:"blocked_user_id"`
	CreatedAt     time.Time `json:"created_at"`
}

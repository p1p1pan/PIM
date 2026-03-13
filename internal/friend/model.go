package friend

import "time"

type Friend struct {
    ID        uint      `gorm:"primaryKey" json:"id"`
    UserID    uint      `gorm:"not null;index" json:"user_id"`
    FriendID  uint      `gorm:"not null;index" json:"friend_id"`
    CreatedAt time.Time `json:"created_at"`
}
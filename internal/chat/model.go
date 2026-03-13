package chat

import "time"

type Message struct {
    ID         uint      `gorm:"primaryKey" json:"id"`
    FromUserID uint      `gorm:"not null" json:"from_user_id"`
    ToUserID   uint      `gorm:"not null" json:"to_user_id"`
    Content    string    `gorm:"type:text;not null" json:"content"`
    CreatedAt  time.Time `json:"created_at"`
}
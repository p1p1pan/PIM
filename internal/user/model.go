package user

import "time"

type User struct {
    ID       uint   `gorm:"primaryKey" json:"id"`
    Username string `gorm:"unique;not null" json:"username"`
    Password string `gorm:"not null" json:"password"`
    Nickname string `json:"nickname"`
    AvatarURL string `json:"avatar_url"`
    Bio       string `json:"bio"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type LoginRequest struct {
    Username string `json:"username" binding:"required"`
    Password string `json:"password" binding:"required"`
}
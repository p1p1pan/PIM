package service

import (
	"github.com/gin-gonic/gin"

	pbconversation "pim/internal/conversation/pb"
	pbfriend "pim/internal/friend/pb"
	pbuser "pim/internal/user/pb"
)

// UserFromPB 把 user protobuf 结构转换为 HTTP 响应对象。
func UserFromPB(u *pbuser.User) gin.H {
	if u == nil {
		return nil
	}
	return gin.H{
		"id":         u.Id,
		"username":   u.Username,
		"nickname":   u.Nickname,
		"avatar_url": u.AvatarUrl,
		"bio":        u.Bio,
		"created_at": u.CreatedAt,
		"updated_at": u.UpdatedAt,
	}
}

// FriendFromPB 把 friend protobuf 结构转换为 HTTP 响应对象。
func FriendFromPB(f *pbfriend.Friend) gin.H {
	if f == nil {
		return nil
	}
	return gin.H{
		"id":         f.Id,
		"user_id":    f.UserId,
		"friend_id":  f.FriendId,
		"created_at": f.CreatedAt,
	}
}

// MessageFromPB 把 message protobuf 结构转换为 HTTP 响应对象。
func MessageFromPB(m *pbconversation.Message) gin.H {
	if m == nil {
		return nil
	}
	return gin.H{
		"id":           m.Id,
		"from_user_id": m.FromUserId,
		"to_user_id":   m.ToUserId,
		"content":      m.Content,
		"created_at":   m.CreatedAt,
	}
}

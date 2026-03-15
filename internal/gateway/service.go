// Package gateway 提供 Gateway 所需的辅助逻辑（如 gRPC 响应转 HTTP/JSON），供 cmd/gateway 调用。
package gateway

import (
	"github.com/gin-gonic/gin"

	pbconversation "pim/internal/conversation/pb"
	pbfriend "pim/internal/friend/pb"
	pbuser "pim/internal/user/pb"
)

// UserFromPB 把 user 服务 gRPC 返回的 *pbuser.User 转成 gin.H，供 Gateway 用 c.JSON 返回给前端。
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

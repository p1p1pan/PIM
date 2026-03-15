package friend

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes 在 auth 上注册 POST/GET /friends；auth 需已挂载 auth.AuthMiddleware()，当前用户 ID 来自 c.Get("userID")。
func RegisterRoutes(r *gin.Engine, auth *gin.RouterGroup, db *gorm.DB) {
	svc := NewService(db)

	// 加好友：从 context 取当前用户 ID，请求体为 friend_id，调用 Service.AddFriend 建立双向关系。
	auth.POST("/friends", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)

		var req struct {
			FriendID uint `json:"friend_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		if err := svc.AddFriend(userID, req.FriendID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "Friend added successfully"})
	})

	// 好友列表：当前用户 ID 来自鉴权中间件，调用 Service.ListFriends 查库返回。
	auth.GET("/friends", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)

		friends, err := svc.ListFriends(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get friends"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"friends": friends})
	})
}
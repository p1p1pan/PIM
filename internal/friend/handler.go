package friend

import (

	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

)

func RegisterRoutes(r *gin.Engine, auth *gin.RouterGroup, db *gorm.DB) {

	// 添加好友
	auth.POST("/friends", func(c *gin.Context) {
		// 获取当前用户ID
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		// 绑定请求参数
		var req struct {
			FriendID uint `json:"friend_id"`
		}	
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 不能添加自己为好友
		if req.FriendID == userID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cannot add yourself as friend"})
			return
		}
		// 保存好友关系到数据库
		if err := db.Create(&Friend{UserID: userID, FriendID: req.FriendID}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add friend"})
			return
		}
		if err := db.Create(&Friend{UserID: req.FriendID, FriendID: userID}).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add friend"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Friend added successfully"})
	})

	// 获取好友列表
	auth.GET("/friends", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		// 查询好友列表
		var friends []Friend
		if err := db.Where("user_id = ?", userID).Find(&friends).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get friends"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"friends": friends})
	})

}
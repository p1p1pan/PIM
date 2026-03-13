package chat

import(
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

)

func RegisterRoutes(r *gin.Engine, auth *gin.RouterGroup, db *gorm.DB) {
	// 获取消息列表
	auth.GET("/messages", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		// 对方ID 从query中拿
		otherIDStr:= c.Query("with")
		if otherIDStr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing or invalid with parameter"})
			return
		}
		otherIDUint, err := strconv.ParseUint(otherIDStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid with parameter"})
			return
		}
		otherID := uint(otherIDUint)
		// 查询消息
		var messages []Message
		if err := db.Where("(from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?)", userID, otherID, otherID, userID).Order("created_at ASC").Find(&messages).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get messages"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"messages": messages})
	})
}
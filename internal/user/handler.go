package user

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterRoutes 在 r 上注册无需登录的路由（注册、登录），在 authGroup 上注册需要登录的路由（/me）。
// authGroup 必须已挂载 auth.AuthMiddleware()，这样 c.Get("userID") 才能取到当前用户 ID。
func RegisterRoutes(r *gin.Engine, authGroup *gin.RouterGroup, db *gorm.DB) {
	svc := NewService(db)

	// 注册：校验用户名密码后调用 Service.Register 写库，不返回 token。
	r.POST("/api/v1/register", func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		u, err := svc.Register(req.Username, req.Password)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "User created successfully",
			"user":    u,
		})
	})

	// 登录：将请求体原样转发到 auth-service，把 auth-service 的 JSON 响应（含 user、token）透传回客户端。
	r.POST("/api/v1/login", func(c *gin.Context) {
		resp, err := http.Post("http://localhost:9000/api/v1/auth/login", "application/json", c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		contentType := resp.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, body)
	})

	// 当前用户信息：userID 由 auth.AuthMiddleware 解析 JWT 后写入 context，这里取出后调 Service.GetByID 查库返回。
	authGroup.GET("/me", func(c *gin.Context) {
		userIDVal, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not found in context"})
			return
		}
		userID, ok := userIDVal.(uint)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user id type"})
			return
		}

		u, err := svc.GetByID(userID)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get user"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"user": u})
	})
}
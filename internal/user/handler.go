package user

import (
	"net/http"
	"time"
	
	"golang.org/x/crypto/bcrypt"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"pim/internal/config"
)


func RegisterRoutes(r *gin.Engine, auth *gin.RouterGroup, db *gorm.DB) {
	// 注册 将用户信息存储到数据库 表名users
	r.POST("/api/v1/register", func(c *gin.Context) {
		var u User
		if err := c.ShouldBindJSON(&u); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 用bcrypt加密密码
		hashed, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		u.Password = string(hashed)

		if err := db.Create(&u).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		
		// 密码不返回给前端
		u.Password = ""
		c.JSON(http.StatusOK, gin.H{
			"message": "User created successfully",
			"user": u,
		})
	})

	// 登录 校验密码 返回用户信息  生成JWT
	r.POST("/api/v1/login", func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 查询用户
		var u User
		if err := db.Where("username = ?", req.Username).First(&u).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		// 用bcrypt比较hash和明文
		if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid password"})
			return
		}

		// 定义 claims
		claims := jwt.MapClaims{
			"sub": u.ID,                // subject，用户 ID
			"username": u.Username,
			"exp": time.Now().Add(24 * time.Hour).Unix(), // 过期时间：24 小时
			"iat": time.Now().Unix(),                     // 签发时间
		}
		// 创建 token
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		// 使用密钥签名
		tokenString, err := token.SignedString([]byte(config.JWTSecret))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
			return
		}

		// 密码不返回给前端
		u.Password = ""
		c.JSON(http.StatusOK, gin.H{
			"message": "Login successful",
			"user": u,
			"token":   tokenString,
		})
	})

	auth.GET("/me", func(c *gin.Context) {
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
		var u User
		if err := db.First(&u, userID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
		u.Password = ""
		c.JSON(http.StatusOK, gin.H{"user": u})
	})

}
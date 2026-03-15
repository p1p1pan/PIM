package main

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbuser "pim/internal/user/pb"
	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
	"pim/internal/conversation"
	"pim/internal/friend"
	"pim/internal/gateway"
	authsvc "pim/internal/auth"
	"pim/internal/user"
)



func main() {
	r := gin.Default()
	// 添加 CORS 中间件
	r.Use(CORSMiddleware())
	// 从 internal/config/config.go 获取配置信息
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost,
		config.DBPort,
		config.DBUser,
		config.DBPassword,
		config.DBName,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("Connected to database")

	// 迁移数据库表
	if err := db.AutoMigrate(&user.User{}, &conversation.Message{}, &friend.Friend{}); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}


	// grpc client 
	// auth service
	authConn, err := grpc.NewClient("localhost:9005", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to auth service: %v", err)
	}
	defer authConn.Close()
	authClient := pbauth.NewAuthServiceClient(authConn)
	// user service
	userConn, err := grpc.NewClient("localhost:9011", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)


	// 受保护路由组：挂载 auth 包提供的鉴权中间件，通过后 c.Get("userID") 可取到当前用户 ID
	authGroup := r.Group("/api/v1", authsvc.GRPCMiddleware(authClient))

	// WebSocket 路由
	r.GET("/ws", authsvc.GRPCMiddleware(authClient), conversation.WebSocketHandler(db))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	// 登录仍 HTTP 转发到 user-service；注册、/me 已改为 gRPC 调 user-service
	r.POST("/api/v1/login", func(c *gin.Context) {
		proxyUserService(c, "POST", "/api/v1/login")
	})
	r.POST("/api/v1/register", func(c *gin.Context) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp, err := userClient.Register(c.Request.Context(), &pbuser.RegisterRequest{
			Username: req.Username,
			Password: req.Password,
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": resp.Message, "user": gateway.UserFromPB(resp.User)})
	})
	authGroup.GET("/me", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		resp, err := userClient.GetByID(c.Request.Context(), &pbuser.GetByIDRequest{UserId: uint64(userID)})
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": gateway.UserFromPB(resp.User)})
	})
	// 转发到 friend-service
	r.POST("/api/v1/friends", func(c *gin.Context) {
		proxyFriendService(c, "POST", "/api/v1/friends")
	})
	r.GET("/api/v1/friends", func(c *gin.Context) {
		proxyFriendService(c, "GET", "/api/v1/friends")
	})
	// 转发到 conversation-service
	authGroup.GET("/messages", func(c *gin.Context) {
		proxyConversationService(c, "GET", "/api/v1/messages")
	})

	// 启动服务器
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	log.Println("Server is running on port 8080")

}

// proxyUserService 将当前请求原样转发到 user-service（端口 9001），仅登录仍使用；注册、/me 已走 gRPC。
func proxyUserService(c *gin.Context, method, path string) {
	url := "http://localhost:9001" + path
	// 创建请求
	req, err := http.NewRequest(method, url, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
		return
	}
	// 设置请求头
	req.Header = c.Request.Header.Clone()
	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send request"})
		return
	}
	// 关闭响应体
	defer resp.Body.Close()
	// 将响应体数据返回给客户端
	c.DataFromReader(resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), io.Reader(resp.Body),
		map[string]string{
			"Content-Type": resp.Header.Get("Content-Type")})
}

// proxyFriendService 将当前请求转发到 friend-service（端口 9002），并把响应原样写回客户端。
func proxyFriendService(c *gin.Context, method, path string) {
	url := "http://localhost:9002" + path
	req, err := http.NewRequest(method, url, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
		return
	}
	// 设置请求头
	req.Header = c.Request.Header.Clone()
	// 发送请求
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send request"})
		return
	}
	// 关闭响应体
	defer resp.Body.Close()
	c.DataFromReader(resp.StatusCode, resp.ContentLength, resp.Header.Get("Content-Type"), io.Reader(resp.Body),
		map[string]string{
			"Content-Type": resp.Header.Get("Content-Type")})
}

// proxyConversationService 将当前请求（含 query）转发到 conversation-service（端口 9003），并把响应原样写回。
func proxyConversationService(c *gin.Context, method, path string) {
	url := "http://localhost:9003" + path
    if qs := c.Request.URL.RawQuery; qs != "" {
        url = url + "?" + qs
    }

    req, err := http.NewRequest(method, url, c.Request.Body)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create request"})
        return
    }
    req.Header = c.Request.Header.Clone()

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send request"})
        return
    }
    defer resp.Body.Close()

    c.DataFromReader(
        resp.StatusCode,
        resp.ContentLength,
        resp.Header.Get("Content-Type"),
        resp.Body,
        map[string]string{"Content-Type": resp.Header.Get("Content-Type")},
    )
}

// CORSMiddleware 设置 CORS 头并对 OPTIONS 预检请求直接 204，避免浏览器跨域请求被拒。
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
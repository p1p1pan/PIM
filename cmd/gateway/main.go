package main

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	pbconversation "pim/internal/conversation/pb"
	pbfriend "pim/internal/friend/pb"
	pbuser "pim/internal/user/pb"

	authsvc "pim/internal/auth"
	"pim/internal/conversation"
	"pim/internal/gateway"
)

func main() {
	r := gin.Default()
	// 添加 CORS 中间件
	r.Use(CORSMiddleware())

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

	// friend service
	friendConn, err := grpc.NewClient("localhost:9012", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to friend service: %v", err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)

	// conversation service
	conversationConn, err := grpc.NewClient("localhost:9013", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to conversation service: %v", err)
	}
	defer conversationConn.Close()
	conversationClient := pbconversation.NewConversationServiceClient(conversationConn)

	// 受保护路由组：挂载 auth 包提供的鉴权中间件，通过后 c.Get("userID") 可取到当前用户 ID
	authGroup := r.Group("/api/v1", authsvc.GRPCMiddleware(authClient))

	// WebSocket 路由
	r.GET("/ws", authsvc.GRPCMiddleware(authClient), conversation.WebSocketHandler(conversationClient))

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	// 登录仍 HTTP 转发到 user-service；注册、/me 已改为 gRPC 调 user-service
	r.POST("/api/v1/login", func(c *gin.Context) {
		// 绑定请求体
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 调用 gRPC 登录
		resp, err := userClient.Login(c.Request.Context(), &pbuser.LoginRequest{
			Username: req.Username,
			Password: req.Password,
		})
		// 处理错误
		if err != nil {
			if st, ok := status.FromError(err); ok {
				if st.Code() == codes.Unauthenticated || st.Code() == codes.InvalidArgument {
					c.JSON(http.StatusUnauthorized, gin.H{"error": st.Message()})
					return
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 返回响应
		c.JSON(http.StatusOK, gin.H{
			"message": resp.Message,
			"user":    gateway.UserFromPB(resp.User),
			"token":   resp.Token,
		})
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
	// 加好友、好友列表：鉴权后取 userID，用 gRPC 调 friend-service
	authGroup.POST("/friends", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		var req struct {
			FriendID uint `json:"friend_id"`
		}
		// 绑定请求体
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 调用 gRPC 添加好友
		_, err := friendClient.AddFriend(c.Request.Context(), &pbfriend.AddFriendRequest{
			UserId:   uint64(userID),
			FriendId: uint64(req.FriendID),
		})
		// 处理错误
		if err != nil {
			if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 返回成功消息
		c.JSON(http.StatusOK, gin.H{"message": "Friend added successfully"})
	})
	authGroup.GET("/friends", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		// 调用 gRPC 获取好友列表
		resp, err := friendClient.ListFriends(c.Request.Context(), &pbfriend.ListFriendsRequest{UserId: uint64(userID)})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 转换为 gin.H 列表
		list := make([]gin.H, 0, len(resp.Friends))
		for _, f := range resp.Friends {
			list = append(list, gateway.FriendFromPB(f))
		}
		// 返回好友列表
		c.JSON(http.StatusOK, gin.H{"friends": list})
	})

	// 转发到 conversation-service
	authGroup.GET("/messages", func(c *gin.Context) {
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		withStr := c.Query("with")
		if withStr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing or invalid with parameter"})
			return
		}
		// 转换为 uint64
		otherID, err := strconv.ParseUint(withStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid with parameter"})
			return
		}
		// 调用 gRPC 获取消息列表
		resp, err := conversationClient.ListMessages(c.Request.Context(), &pbconversation.ListMessagesRequest{
			UserId:  uint64(userID),
			OtherId: otherID,
		})
		// 处理错误
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 转换为 gin.H 列表
		list := make([]gin.H, 0, len(resp.Messages))
		for _, m := range resp.Messages {
			list = append(list, gateway.MessageFromPB(m))
		}
		// 返回消息列表
		c.JSON(http.StatusOK, gin.H{"messages": list})
	})

	// 启动服务器
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
	log.Println("Server is running on port 8080")

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

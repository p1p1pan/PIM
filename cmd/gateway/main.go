package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	pbconversation "pim/internal/conversation/pb"
	pbfriend "pim/internal/friend/pb"
	pbuser "pim/internal/user/pb"

	authsvc "pim/internal/auth"
	"pim/internal/config"
	"pim/internal/conversation"
	"pim/internal/gateway"
)

const gatewayNodeID = "gateway-1"

func main() {
	r := gin.Default()
	// 添加 CORS 中间件
	r.Use(CORSMiddleware())
	// 添加 trace 中间件
	r.Use(TraceMiddleware())
	// redis client
	var redisClient *redis.Client
	redisClient = redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	log.Printf("RedisAddr from config: %s", config.RedisAddr)
	defer redisClient.Close()

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

	// WebSocket 路由：包一层以打印 trace_id
	r.GET("/ws", authsvc.GRPCMiddleware(authClient), func(c *gin.Context) {
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] GET /ws (upgrade)", traceID)
		// 1. 从鉴权中间件拿到 userID
		userIDVal, ok := c.Get("userID")
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
			return
		}
		userID, ok := userIDVal.(uint)
		if !ok || userID == 0 {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid user id"})
			return
		}
		// 2. 生成 connId（当前连接在本节点内的唯一标识）
		connID := uuid.NewString()
		// 3. 写入 Redis 注册表：ws:conn:{user_id} = {gatewayNodeID}:{connID}
		key := fmt.Sprintf("ws:conn:%d", userID)
		val := fmt.Sprintf("%s:%s", gatewayNodeID, connID)
		if err := redisClient.Set(context.Background(), key, val, 0).Err(); err != nil {
			log.Printf("[trace=%v] failed to set ws registry for user %d: %v", traceID, userID, err)
			// 注册表失败不阻止连接建立，但打日志
		} else {
			log.Printf("[trace=%v] registered ws conn for user %d: %s", traceID, userID, val)
		}
		// 4. 确保连接结束时删除 Redis 注册表
		defer func() {
			if err := redisClient.Del(context.Background(), key).Err(); err != nil {
				log.Printf("[trace=%v] failed to delete ws registry for user %d: %v", traceID, userID, err)
			} else {
				log.Printf("[trace=%v] deleted ws registry for user %d", traceID, userID)
			}
		}()
		conversation.WebSocketHandler(conversationClient)(c)
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	// 登录仍 HTTP 转发到 user-service；注册、/me 已改为 gRPC 调 user-service
	r.POST("/api/v1/login", func(c *gin.Context) {
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] POST /api/v1/login", traceID)
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 调用 Auth 的 Login
		resp, err := authClient.Login(ctxWithTrace(c), &pbauth.LoginRequest{
			Username: req.Username,
			Password: req.Password,
		})
		// 处理错误
		if err != nil {
			if st, ok := status.FromError(err); ok {
				switch st.Code() {
				case codes.InvalidArgument:
					c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
					return
				case codes.Unauthenticated:
					c.JSON(http.StatusUnauthorized, gin.H{"error": st.Message()})
					return
				}
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 返回响应
		c.JSON(http.StatusOK, gin.H{
			"message": resp.GetMessage(),
			"user": gin.H{
				"id":       resp.GetUserId(),
				"username": resp.GetUsername(),
			},
			"token": resp.GetAccessToken(),
		})
	})
	r.POST("/api/v1/register", func(c *gin.Context) {
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] POST /api/v1/register", traceID)
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp, err := userClient.Register(ctxWithTrace(c), &pbuser.RegisterRequest{
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
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] GET /api/v1/me", traceID)
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		resp, err := userClient.GetByID(ctxWithTrace(c), &pbuser.GetByIDRequest{UserId: uint64(userID)})
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
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] POST /api/v1/friends", traceID)
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
		_, err := friendClient.AddFriend(ctxWithTrace(c), &pbfriend.AddFriendRequest{
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
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] GET /api/v1/friends", traceID)
		userIDVal, _ := c.Get("userID")
		userID := userIDVal.(uint)
		// 调用 gRPC 获取好友列表
		resp, err := friendClient.ListFriends(ctxWithTrace(c), &pbfriend.ListFriendsRequest{UserId: uint64(userID)})
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
		traceID, _ := c.Get("trace_id")
		log.Printf("[trace=%v] GET /api/v1/messages", traceID)
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
		resp, err := conversationClient.ListMessages(ctxWithTrace(c), &pbconversation.ListMessagesRequest{
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

// TraceMiddleware 为每个请求生成或透传 trace_id，写入 Gin context。
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Request.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = uuid.NewString()
		}
		// 放到 Gin context，后续 handler 可以 c.Get("trace_id")
		c.Set("trace_id", traceID)
		// 也写回响应头，方便前端或日志关联
		c.Writer.Header().Set("X-Trace-Id", traceID)
		c.Next()
	}
}

// ctxWithTrace 从 Gin context 取 trace_id，写入 gRPC metadata。
func ctxWithTrace(c *gin.Context) context.Context {
	traceID, _ := c.Get("trace_id")
	idStr, _ := traceID.(string)
	if idStr == "" {
		return c.Request.Context()
	}
	md := metadata.Pairs("x-trace-id", idStr)
	return metadata.NewOutgoingContext(c.Request.Context(), md)
}

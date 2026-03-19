package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authhandler "pim/internal/auth/handler"
	pbauth "pim/internal/auth/pb"
	conversationhandler "pim/internal/conversation/handler"
	pbconversation "pim/internal/conversation/pb"
	pbfriend "pim/internal/friend/pb"
	gatewaymodel "pim/internal/gateway/model"
	gatewayservice "pim/internal/gateway/service"
	"pim/internal/mq/kafka"
	pbuser "pim/internal/user/pb"
)

const gatewayNodeID = "gateway-1"

// HTTPServer 聚合 Gateway HTTP 层依赖。
type HTTPServer struct {
	authClient         pbauth.AuthServiceClient
	userClient         pbuser.UserServiceClient
	friendClient       pbfriend.FriendServiceClient
	conversationClient pbconversation.ConversationServiceClient
	redisClient        *redis.Client
	kafkaBrokers       []string
}

// NewHTTPServer 创建 Gateway HTTPServer。
func NewHTTPServer(
	authClient pbauth.AuthServiceClient,
	userClient pbuser.UserServiceClient,
	friendClient pbfriend.FriendServiceClient,
	conversationClient pbconversation.ConversationServiceClient,
	redisClient *redis.Client,
	kafkaBrokers []string,
) *HTTPServer {
	return &HTTPServer{
		authClient:         authClient,
		userClient:         userClient,
		friendClient:       friendClient,
		conversationClient: conversationClient,
		redisClient:        redisClient,
		kafkaBrokers:       kafkaBrokers,
	}
}

// CORSMiddleware 统一设置跨域响应头。
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

// TraceMiddleware 为请求注入或透传 trace_id。
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Request.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = uuid.NewString()
		}
		c.Set("trace_id", traceID)
		c.Writer.Header().Set("X-Trace-Id", traceID)
		c.Next()
	}
}

// ctxWithTrace 将 Gin 上下文中的 trace_id 透传到 gRPC metadata。
func ctxWithTrace(c *gin.Context) context.Context {
	traceID, _ := c.Get("trace_id")
	idStr, _ := traceID.(string)
	if idStr == "" {
		return c.Request.Context()
	}
	md := metadata.Pairs("x-trace-id", idStr)
	return metadata.NewOutgoingContext(c.Request.Context(), md)
}

// RegisterRoutes 注册 Gateway 全部对外 HTTP/WS 路由。
func (s *HTTPServer) RegisterRoutes(r *gin.Engine) {
	authGroup := r.Group("/api/v1", authhandler.GRPCMiddleware(s.authClient))

	r.GET("/ws", authhandler.GRPCMiddleware(s.authClient), s.handleWS)
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"message": "ok"}) })
	r.POST("/api/v1/login", s.handleLogin)
	r.POST("/api/v1/register", s.handleRegister)

	authGroup.GET("/me", s.handleMe)
	authGroup.POST("/friends", s.handleAddFriend)
	authGroup.GET("/friends", s.handleListFriends)
	authGroup.GET("/conversations", s.handleListConversations)
	authGroup.GET("/messages", s.handleListMessages)
	authGroup.PUT("/conversations/:peer_id/read", s.handleConversationRead)
}

// handleWS 处理 WebSocket 升级、在线连接登记与上行转发。
func (s *HTTPServer) handleWS(c *gin.Context) {
	traceID, _ := c.Get("trace_id")
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
	connID := uuid.NewString()
	key := fmt.Sprintf("ws:conn:%d", userID)
	val := fmt.Sprintf("%s:%s", gatewayNodeID, connID)
	if err := s.redisClient.Set(context.Background(), key, val, 0).Err(); err != nil {
		log.Printf("[trace=%v] failed to set ws registry for user %d: %v", traceID, userID, err)
	}
	defer func() { _ = s.redisClient.Del(context.Background(), key).Err() }()
	kProducer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: s.kafkaBrokers})
	conversationhandler.WebSocketHandler(s.conversationClient, kProducer)(c)
}

// handleLogin 处理登录请求并调用 Auth gRPC。
func (s *HTTPServer) handleLogin(c *gin.Context) {
	var req gatewaymodel.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := s.authClient.Login(ctxWithTrace(c), &pbauth.LoginRequest{Username: req.Username, Password: req.Password})
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
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage(), "user": gin.H{"id": resp.GetUserId(), "username": resp.GetUsername()}, "token": resp.GetAccessToken()})
}

// handleRegister 处理注册请求并调用 User gRPC。
func (s *HTTPServer) handleRegister(c *gin.Context) {
	var req gatewaymodel.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	resp, err := s.userClient.Register(ctxWithTrace(c), &pbuser.RegisterRequest{Username: req.Username, Password: req.Password})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.Message, "user": gatewayservice.UserFromPB(resp.User)})
}

// handleMe 获取当前登录用户信息。
func (s *HTTPServer) handleMe(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	resp, err := s.userClient.GetByID(ctxWithTrace(c), &pbuser.GetByIDRequest{UserId: uint64(userID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": gatewayservice.UserFromPB(resp.User)})
}

// handleAddFriend 处理添加好友请求。
func (s *HTTPServer) handleAddFriend(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	var req gatewaymodel.AddFriendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	_, err := s.friendClient.AddFriend(ctxWithTrace(c), &pbfriend.AddFriendRequest{UserId: uint64(userID), FriendId: uint64(req.FriendID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Friend added successfully"})
}

// handleListFriends 查询好友列表。
func (s *HTTPServer) handleListFriends(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	resp, err := s.friendClient.ListFriends(ctxWithTrace(c), &pbfriend.ListFriendsRequest{UserId: uint64(userID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	list := make([]gin.H, 0, len(resp.Friends))
	for _, f := range resp.Friends {
		list = append(list, gatewayservice.FriendFromPB(f))
	}
	c.JSON(http.StatusOK, gin.H{"friends": list})
}

// handleListConversations 查询会话列表。
func (s *HTTPServer) handleListConversations(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	resp, err := s.conversationClient.ListConversations(ctxWithTrace(c), &pbconversation.ListConversationsRequest{UserId: uint64(userID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	list := make([]gin.H, 0, len(resp.Conversations))
	for _, conv := range resp.Conversations {
		ua, ub := conv.GetUserA(), conv.GetUserB()
		current := uint64(userID)
		var peer uint64
		if current == ua {
			peer = ub
		} else {
			peer = ua
		}
		list = append(list, gin.H{"id": conv.GetId(), "peer_id": peer, "last_message_id": conv.GetLastMessageId(), "last_seq": conv.GetLastSeq(), "last_message_at": conv.GetLastMessageAt()})
	}
	c.JSON(http.StatusOK, gin.H{"conversations": list})
}

// handleListMessages 查询历史消息和当前未读计数。
func (s *HTTPServer) handleListMessages(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	withStr := c.Query("with")
	otherID, err := strconv.ParseUint(withStr, 10, 64)
	if err != nil || withStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid with parameter"})
		return
	}
	resp, err := s.conversationClient.ListMessages(ctxWithTrace(c), &pbconversation.ListMessagesRequest{UserId: uint64(userID), OtherId: otherID})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	list := make([]gin.H, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		list = append(list, gatewayservice.MessageFromPB(m))
	}
	userA, userB := uint64(userID), otherID
	if userA > userB {
		userA, userB = userB, userA
	}
	convKey := fmt.Sprintf("%d:%d", userA, userB)
	unreadKey := fmt.Sprintf("msg:unread:%d:%s", userB, convKey)
	var unread int64
	if s.redisClient != nil {
		if v, err := s.redisClient.Get(context.Background(), unreadKey).Result(); err == nil {
			if n, err2 := strconv.ParseInt(v, 10, 64); err2 == nil {
				unread = n
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"messages": list, "unread": unread})
}

// handleConversationRead 发布会话已读事件到 Kafka。
func (s *HTTPServer) handleConversationRead(c *gin.Context) {
	traceID, _ := c.Get("trace_id")
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	peerStr := c.Param("peer_id")
	peer64, err := strconv.ParseUint(peerStr, 10, 64)
	if err != nil || peer64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid peer_id"})
		return
	}
	peerID := uint(peer64)
	userA, userB := userID, peerID
	if userA > userB {
		userA, userB = userB, userA
	}
	convKey := fmt.Sprintf("%d:%d", userA, userB)
	evt := gatewaymodel.ConversationReadEvent{
		TraceID:        fmt.Sprint(traceID),
		UserID:         userID,
		PeerID:         peerID,
		ConversationID: convKey,
	}
	data, err := json.Marshal(evt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to marshal read event"})
		return
	}
	kProducer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: s.kafkaBrokers})
	if err := kProducer.SendMessage(context.Background(), "im-message-read", "", data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "read event queued", "conversation_id": convKey})
}

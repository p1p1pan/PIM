package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
	conversationhandler "pim/internal/conversation/handler"
	pbconversation "pim/internal/conversation/pb"
	pbfile "pim/internal/file/pb"
	pbfriend "pim/internal/friend/pb"
	gatewaymodel "pim/internal/gateway/model"
	gatewayservice "pim/internal/gateway/service"
	pbgroup "pim/internal/group/pb"
	"pim/internal/kit/mq/kafka"
	pbuser "pim/internal/user/pb"
)

// HTTPServer 聚合 Gateway HTTP 层依赖。
type HTTPServer struct {
	authClient         pbauth.AuthServiceClient
	userClient         pbuser.UserServiceClient
	friendClient       pbfriend.FriendServiceClient
	conversationClient pbconversation.ConversationServiceClient
	groupClient        pbgroup.GroupServiceClient
	fileClient         pbfile.FileServiceClient
	redisClient        *redis.Client
	kafkaBrokers       []string
	kafkaProducer      *kafka.Producer
	groupKafkaDispatch *groupKafkaDispatcher
	nodeID             string
	logServiceBaseURL  string
	fileServiceBaseURL string
	apiRouteCatalog    []string
}

// NewHTTPServer 创建 Gateway HTTPServer。
func NewHTTPServer(
	authClient pbauth.AuthServiceClient,
	userClient pbuser.UserServiceClient,
	friendClient pbfriend.FriendServiceClient,
	conversationClient pbconversation.ConversationServiceClient,
	groupClient pbgroup.GroupServiceClient,
	fileClient pbfile.FileServiceClient,
	redisClient *redis.Client,
	kafkaBrokers []string,
	nodeID string,
	logServiceBaseURL string,
	fileServiceBaseURL string,
) *HTTPServer {
	if strings.TrimSpace(nodeID) == "" {
		nodeID = "gateway-1"
	}
	kProducer := kafka.NewProducer(kafka.DefaultProducerConfig(kafkaBrokers))
	dispatcher := newGroupKafkaDispatcher(kProducer)
	srv := &HTTPServer{
		authClient:         authClient,
		userClient:         userClient,
		friendClient:       friendClient,
		conversationClient: conversationClient,
		groupClient:        groupClient,
		fileClient:         fileClient,
		redisClient:        redisClient,
		kafkaBrokers:       kafkaBrokers,
		kafkaProducer:      kProducer,
		groupKafkaDispatch: dispatcher,
		nodeID:             nodeID,
		logServiceBaseURL:  strings.TrimRight(logServiceBaseURL, "/"),
		fileServiceBaseURL: strings.TrimRight(fileServiceBaseURL, "/"),
	}
	if config.GatewayGroupMemberPrewarmEnabled && redisClient != nil {
		go func() {
			timeout := time.Duration(config.GatewayGroupMemberPrewarmTimeoutSec) * time.Second
			if timeout <= 0 {
				timeout = 20 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			srv.prewarmGroupMembersLocalIndex(ctx, config.GatewayGroupMemberPrewarmLimit)
		}()
	}
	return srv
}

// Close 关闭 HTTPServer 关联资源。
func (s *HTTPServer) Close() error {
	if s == nil {
		return nil
	}
	if s.groupKafkaDispatch != nil {
		s.groupKafkaDispatch.stop()
	}
	if s.kafkaProducer == nil {
		return nil
	}
	return s.kafkaProducer.Close()
}

// CORSMiddleware 统一设置跨域响应头。
func CORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Trace-Id")
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

// handleWS 处理 WebSocket 升级、在线连接登记与上行转发。
func (s *HTTPServer) handleWS(c *gin.Context) {
	traceID, _ := c.Get("trace_id")
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	connID := uuid.NewString()
	key := fmt.Sprintf("ws:conn:%d", userID)
	val := fmt.Sprintf("%s:%s", s.nodeID, connID)
	// 建立连接归属映射：消费端据此判断是否应由本节点执行实时推送。
	if err := s.redisClient.Set(context.Background(), key, val, 0).Err(); err != nil {
		log.Printf("[trace=%v] failed to set ws registry for user %d: %v", traceID, userID, err)
	}
	// 连接结束时清理映射，避免“离线用户被误判在线”。
	defer func() { _ = s.redisClient.Del(context.Background(), key).Err() }()
	conversationhandler.WebSocketHandler(s.conversationClient, s.kafkaProducer)(c)
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
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	resp, err := s.userClient.GetByID(ctxWithTrace(c), &pbuser.GetByIDRequest{UserId: uint64(userID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": gatewayservice.UserFromPB(resp.User)})
}

// handleListConversations 查询会话列表。
func (s *HTTPServer) handleListConversations(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
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
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
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
	// 会话 ID 始终按小->大拼接，确保读事件和未读键使用统一主键。
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
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
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
	if err := s.kafkaProducer.SendMessage(context.Background(), "im-message-read", "", data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "conversation read queued", "", map[string]interface{}{"conversation_id": convKey})
	c.JSON(http.StatusOK, gin.H{"message": "read event queued", "conversation_id": convKey})
}

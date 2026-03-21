package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authhandler "pim/internal/auth/handler"
	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
	conversationhandler "pim/internal/conversation/handler"
	pbconversation "pim/internal/conversation/pb"
	pbfile "pim/internal/file/pb"
	pbfriend "pim/internal/friend/pb"
	gatewaymodel "pim/internal/gateway/model"
	gatewayservice "pim/internal/gateway/service"
	pbgroup "pim/internal/group/pb"
	logkit "pim/internal/log/kit"
	logmodel "pim/internal/log/model"
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
	groupClient        pbgroup.GroupServiceClient
	fileClient         pbfile.FileServiceClient
	redisClient        *redis.Client
	kafkaBrokers       []string
	kafkaProducer      *kafka.Producer
	logServiceBaseURL  string
	fileServiceBaseURL string
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
	logServiceBaseURL string,
	fileServiceBaseURL string,
) *HTTPServer {
	kProducer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: kafkaBrokers})
	return &HTTPServer{
		authClient:         authClient,
		userClient:         userClient,
		friendClient:       friendClient,
		conversationClient: conversationClient,
		groupClient:        groupClient,
		fileClient:         fileClient,
		redisClient:        redisClient,
		kafkaBrokers:       kafkaBrokers,
		kafkaProducer:      kProducer,
		logServiceBaseURL:  strings.TrimRight(logServiceBaseURL, "/"),
		fileServiceBaseURL: strings.TrimRight(fileServiceBaseURL, "/"),
	}
}

// Close 关闭 HTTPServer 关联资源。
func (s *HTTPServer) Close() error {
	if s == nil || s.kafkaProducer == nil {
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

// AccessLogMiddleware 将网关请求访问日志异步写入 log-topic。
func (s *HTTPServer) AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if s == nil || s.kafkaProducer == nil {
			return
		}
		traceID := c.GetString("trace_id")
		entry := logmodel.Log{
			TS:        time.Now(),
			Level:     "info",
			Service:   "gateway",
			TraceID:   traceID,
			Msg:       "http access",
			Path:      c.FullPath(),
			LatencyMS: time.Since(start).Milliseconds(),
			ErrorCode: fmt.Sprintf("%d", c.Writer.Status()),
		}
		if entry.Path == "" {
			entry.Path = c.Request.URL.Path
		}
		if uid, ok := c.Get("userID"); ok {
			if id, ok := uid.(uint); ok {
				entry.UserID = uint64(id)
			}
		}
		entry, ok := logkit.ApplyPolicy(entry, config.LogInfoSamplePct)
		if !ok {
			return
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return
		}
		_ = s.kafkaProducer.SendMessage(context.Background(), "log-topic", "", data)
	}
}

// emitBizLog 将关键业务事件写入 log-topic，供 log-service 聚合检索。
func (s *HTTPServer) emitBizLog(c *gin.Context, msg string, eventID string, extra map[string]interface{}) {
	if s == nil || s.kafkaProducer == nil {
		return
	}
	entry := logmodel.Log{
		TS:      time.Now(),
		Level:   "info",
		Service: "gateway",
		TraceID: c.GetString("trace_id"),
		Msg:     msg,
		Path:    c.FullPath(),
		EventID: eventID,
	}
	if entry.Path == "" {
		entry.Path = c.Request.URL.Path
	}
	if uid, ok := c.Get("userID"); ok {
		if id, ok := uid.(uint); ok {
			entry.UserID = uint64(id)
		}
	}
	if extra != nil {
		if gid, ok := extra["group_id"].(uint64); ok {
			entry.GroupID = gid
		}
		if cid, ok := extra["conversation_id"].(string); ok {
			entry.ConversationID = cid
		}
	}
	entry, ok := logkit.ApplyPolicy(entry, config.LogInfoSamplePct)
	if !ok {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = s.kafkaProducer.SendMessage(context.Background(), "log-topic", "", data)
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
	authGroup.GET("/friends", s.handleListFriends)
	authGroup.GET("/conversations", s.handleListConversations)
	authGroup.GET("/messages", s.handleListMessages)
	authGroup.PUT("/conversations/:peer_id/read", s.handleConversationRead)
	authGroup.POST("/friends/requests", s.handleSendFriendRequest)
	authGroup.POST("/friends/requests/:id/approve", s.handleApproveFriendRequest)
	authGroup.POST("/friends/requests/:id/reject", s.handleRejectFriendRequest)
	authGroup.POST("/friends/blacklist/:user_id", s.handleBlockUser)
	authGroup.DELETE("/friends/:user_id", s.handleDeleteFriend)
	authGroup.GET("/friends/requests/incoming", s.handleListIncomingFriendRequests)
	authGroup.GET("/friends/requests/outgoing", s.handleListOutgoingFriendRequests)
	authGroup.POST("/groups", s.handleCreateGroup)
	authGroup.GET("/groups", s.handleListMyGroups)
	authGroup.GET("/groups/conversations", s.handleListGroupConversations)
	authGroup.GET("/groups/:id", s.handleGetGroup)
	authGroup.PUT("/groups/:id", s.handleUpdateGroup)
	authGroup.POST("/groups/:id/leave", s.handleLeaveGroup)
	authGroup.POST("/groups/:id/disband", s.handleDisbandGroup)
	authGroup.POST("/groups/:id/transfer-owner", s.handleTransferGroupOwner)
	authGroup.POST("/groups/:id/members", s.handleAddGroupMember)
	authGroup.DELETE("/groups/:id/members/:user_id", s.handleRemoveGroupMember)
	authGroup.GET("/groups/:id/members", s.handleListGroupMembers)
	authGroup.POST("/groups/:id/messages", s.handleSendGroupMessage)
	authGroup.GET("/groups/:id/messages", s.handleListGroupMessages)
	authGroup.PUT("/groups/:id/read", s.handleMarkGroupRead)
	authGroup.POST("/files/prepare", s.handleFilePrepare)
	authGroup.POST("/files/:id/commit", s.handleFileCommit)
	authGroup.GET("/files/:id", s.handleFileGet)
	authGroup.GET("/logs/trace/:trace_id", s.handleLogsByTrace)
	authGroup.GET("/logs/search", s.handleLogsByEvent)
	authGroup.GET("/logs/filter", s.handleLogsFilter)
	authGroup.GET("/admin/health", s.handleAdminHealth)
	authGroup.GET("/admin/metrics", s.handleAdminMetrics)
	authGroup.GET("/admin/file-scan/dlq", s.handleFileScanDLQList)
	authGroup.POST("/admin/file-scan/dlq/:file_id/replay", s.handleFileScanDLQReplay)
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
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	resp, err := s.userClient.GetByID(ctxWithTrace(c), &pbuser.GetByIDRequest{UserId: uint64(userID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": gatewayservice.UserFromPB(resp.User)})
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
	if err := s.kafkaProducer.SendMessage(context.Background(), "im-message-read", "", data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "conversation read queued", "", map[string]interface{}{"conversation_id": convKey})
	c.JSON(http.StatusOK, gin.H{"message": "read event queued", "conversation_id": convKey})
}

// handleSendFriendRequest 发送好友申请。
func (s *HTTPServer) handleSendFriendRequest(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	fromUserID := userIDVal.(uint)
	// 非法请求
	var req gatewaymodel.SendFriendRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ToUserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	// 发送好友申请
	resp, err := s.friendClient.SendFriendRequest(ctxWithTrace(c), &pbfriend.SendFriendRequestRequest{
		FromUserId: uint64(fromUserID),
		ToUserId:   uint64(req.ToUserID),
		Remark:     req.Remark,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 尝试实时通知被申请方（若对方当前不在线，忽略推送错误，不影响主流程）。
	s.pushFriendRequestEvent(
		c,
		req.ToUserID,
		"friend_request_created",
		resp.GetRequestId(),
		uint64(fromUserID),
		uint64(req.ToUserID),
		resp.GetStatus(),
		req.Remark,
	)
	// 返回结果
	c.JSON(http.StatusOK, gin.H{
		"request_id": resp.GetRequestId(),
		"status":     resp.GetStatus(),
	})
}

// handleApproveFriendRequest 同意好友申请。
func (s *HTTPServer) handleApproveFriendRequest(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	operatorUserID := userIDVal.(uint)
	// 非法请求
	reqIDStr := c.Param("id")
	reqID, err := strconv.ParseUint(reqIDStr, 10, 64)
	if err != nil || reqID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request id"})
		return
	}
	// 查询好友申请详情
	detail, err := s.friendClient.GetFriendRequestByID(ctxWithTrace(c), &pbfriend.GetFriendRequestByIDRequest{
		RequestId: reqID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.NotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			}
			if st.Code() == codes.InvalidArgument {
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 转换为 gin.H 对象
	item := detail.GetItem()
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "friend request not found"})
		return
	}

	// 同意好友申请
	resp, err := s.friendClient.ApproveFriendRequest(ctxWithTrace(c), &pbfriend.ApproveFriendRequestRequest{
		RequestId:      reqID,
		OperatorUserId: uint64(operatorUserID),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 成功后给双方推 updated 事件
	fromID := item.GetFromUserId()
	toID := item.GetToUserId()
	s.pushFriendRequestEvent(
		c,
		uint(toID),
		"friend_request_updated",
		reqID,
		fromID,
		toID,
		"accepted",
		item.GetRemark(),
	)
	s.pushFriendRequestEvent(
		c,
		uint(fromID),
		"friend_request_updated",
		reqID,
		fromID,
		toID,
		"accepted",
		item.GetRemark(),
	)
	// 返回结果
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

// handleRejectFriendRequest 拒绝好友申请。
func (s *HTTPServer) handleRejectFriendRequest(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	operatorUserID := userIDVal.(uint)
	// 非法请求
	reqIDStr := c.Param("id")
	reqID, err := strconv.ParseUint(reqIDStr, 10, 64)
	if err != nil || reqID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request id"})
		return
	}
	// 先查申请详情
	detail, err := s.friendClient.GetFriendRequestByID(ctxWithTrace(c), &pbfriend.GetFriendRequestByIDRequest{
		RequestId: reqID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.NotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			}
			if st.Code() == codes.InvalidArgument {
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 转换为 gin.H 对象
	item := detail.GetItem()
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "friend request not found"})
		return
	}
	// 拒绝好友申请
	resp, err := s.friendClient.RejectFriendRequest(ctxWithTrace(c), &pbfriend.RejectFriendRequestRequest{
		RequestId:      reqID,
		OperatorUserId: uint64(operatorUserID),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 成功后给双方推 updated 事件
	fromID := item.GetFromUserId()
	toID := item.GetToUserId()
	s.pushFriendRequestEvent(
		c,
		uint(toID),
		"friend_request_updated",
		reqID,
		fromID,
		toID,
		"rejected",
		item.GetRemark(),
	)
	s.pushFriendRequestEvent(
		c,
		uint(fromID),
		"friend_request_updated",
		reqID,
		fromID,
		toID,
		"rejected",
		item.GetRemark(),
	)
	// 返回结果
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

// handleBlockUser 兼容旧接口：内部语义已改为删除好友。
func (s *HTTPServer) handleBlockUser(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	// 非法请求
	targetStr := c.Param("user_id")
	targetID, err := strconv.ParseUint(targetStr, 10, 64)
	if err != nil || targetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	// 删除好友关系（兼容旧路径）。
	resp, err := s.friendClient.BlockUser(ctxWithTrace(c), &pbfriend.BlockUserRequest{
		UserId:        uint64(userID),
		BlockedUserId: targetID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

// handleDeleteFriend 删除好友（推荐新接口）。
func (s *HTTPServer) handleDeleteFriend(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	targetStr := c.Param("user_id")
	targetID, err := strconv.ParseUint(targetStr, 10, 64)
	if err != nil || targetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	resp, err := s.friendClient.BlockUser(ctxWithTrace(c), &pbfriend.BlockUserRequest{
		UserId:        uint64(userID),
		BlockedUserId: targetID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

// handleListIncomingFriendRequests 查询“我收到的”好友申请列表。
func (s *HTTPServer) handleListIncomingFriendRequests(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	// 非法请求
	var q gatewaymodel.ListFriendRequestsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	// 查询“我收到的”好友申请
	resp, err := s.friendClient.ListIncomingFriendRequests(ctxWithTrace(c), &pbfriend.ListIncomingFriendRequestsRequest{
		UserId: uint64(userID),
		Status: q.Status,
		Cursor: q.Cursor,
		Limit:  q.Limit,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.InvalidArgument {
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 转换为 gin.H 对象
	items := make([]gin.H, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, gin.H{
			"request_id":   it.GetRequestId(),
			"from_user_id": it.GetFromUserId(),
			"to_user_id":   it.GetToUserId(),
			"status":       it.GetStatus(),
			"remark":       it.GetRemark(),
			"created_at":   it.GetCreatedAt(),
			"updated_at":   it.GetUpdatedAt(),
		})
	}
	// 返回结果
	c.JSON(http.StatusOK, gin.H{
		"items":       items,
		"next_cursor": resp.GetNextCursor(),
		"has_more":    resp.GetHasMore(),
	})
}

// handleListOutgoingFriendRequests 查询“我发出的”好友申请列表。
func (s *HTTPServer) handleListOutgoingFriendRequests(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	// 非法请求
	var q gatewaymodel.ListFriendRequestsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	// 查询“我发出的”好友申请
	resp, err := s.friendClient.ListOutgoingFriendRequests(ctxWithTrace(c), &pbfriend.ListOutgoingFriendRequestsRequest{
		UserId: uint64(userID),
		Status: q.Status,
		Cursor: q.Cursor,
		Limit:  q.Limit,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			if st.Code() == codes.InvalidArgument {
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 转换为 gin.H 对象
	items := make([]gin.H, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, gin.H{
			"request_id":   it.GetRequestId(),
			"from_user_id": it.GetFromUserId(),
			"to_user_id":   it.GetToUserId(),
			"status":       it.GetStatus(),
			"remark":       it.GetRemark(),
			"created_at":   it.GetCreatedAt(),
			"updated_at":   it.GetUpdatedAt(),
		})
	}
	// 返回结果
	c.JSON(http.StatusOK, gin.H{
		"items":       items,
		"next_cursor": resp.GetNextCursor(),
		"has_more":    resp.GetHasMore(),
	})
}

// pushFriendRequestEvent 向指定用户推送好友申请事件。
func (s *HTTPServer) pushFriendRequestEvent(c *gin.Context, toUserID uint, eventType string, requestID uint64, fromUserID uint64, targetUserID uint64, status string, remark string) {
	payload, _ := json.Marshal(gin.H{
		"type":         eventType, // friend_request_created / friend_request_updated
		"request_id":   requestID,
		"from_user_id": fromUserID,
		"to_user_id":   targetUserID,
		"status":       status,
		"remark":       remark,
		"ts":           time.Now().Unix(),
	})
	if err := conversationhandler.PushToUser(toUserID, 0, string(payload)); err != nil {
		log.Printf("[trace=%v] push friend event failed to user=%d: %v", c.GetString("trace_id"), toUserID, err)
	}
}

// pushGroupEvent 向指定用户推送群事件（失败仅记日志，不影响主流程）。
func (s *HTTPServer) pushGroupEvent(c *gin.Context, toUserID uint, eventType string, groupID uint64, operatorUserID uint64, extra map[string]interface{}) {
	payload := map[string]interface{}{
		"type":             eventType,
		"group_id":         groupID,
		"operator_user_id": operatorUserID,
		"ts":               time.Now().Unix(),
	}
	for k, v := range extra {
		payload[k] = v
	}
	data, _ := json.Marshal(payload)
	if err := conversationhandler.PushToUser(toUserID, 0, string(data)); err != nil {
		log.Printf("[trace=%s] push group event failed: event=%s to=%d gid=%d err=%v", c.GetString("trace_id"), eventType, toUserID, groupID, err)
	}
}

// listGroupMemberIDs 查询群成员 user_id 列表，失败返回空切片并记日志。
func (s *HTTPServer) listGroupMemberIDs(c *gin.Context, groupID uint64) []uint64 {
	resp, err := s.groupClient.ListMembers(ctxWithTrace(c), &pbgroup.ListMembersRequest{GroupId: groupID})
	if err != nil {
		log.Printf("[trace=%s] list group members for push failed: gid=%d err=%v", c.GetString("trace_id"), groupID, err)
		return nil
	}
	ids := make([]uint64, 0, len(resp.GetMembers()))
	for _, m := range resp.GetMembers() {
		ids = append(ids, m.GetUserId())
	}
	return ids
}

// appendGroupSystemMessage 追加系统消息到群历史（失败仅记日志）。
func (s *HTTPServer) appendGroupSystemMessage(c *gin.Context, groupID uint64, operatorUserID uint64, eventType string, content string, eventID string) {
	if eventID == "" {
		eventID = uuid.NewString()
	}
	_, err := s.groupClient.AppendSystemMessage(ctxWithTrace(c), &pbgroup.AppendSystemMessageRequest{
		GroupId:        groupID,
		OperatorUserId: operatorUserID,
		EventType:      eventType,
		Content:        content,
		EventId:        eventID,
	})
	if err != nil {
		log.Printf("[trace=%s] append group system message failed: gid=%d event=%s err=%v", c.GetString("trace_id"), groupID, eventType, err)
	}
}

// handleCreateGroup 创建群。
func (s *HTTPServer) handleCreateGroup(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	operatorUserID := userIDVal.(uint)
	// 非法请求  name 不能为空
	var req gatewaymodel.CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	// 创建群
	resp, err := s.groupClient.CreateGroup(ctxWithTrace(c), &pbgroup.CreateGroupRequest{
		OwnerUserId:   uint64(operatorUserID),
		Name:          req.Name,
		MemberUserIds: req.MemberUserIDs,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 转换为 gin.H 对象
	g := resp.GetGroup()
	// 实时事件：通知群成员（创建时成员已落库）。
	memberIDs := s.listGroupMemberIDs(c, g.GetId())
	s.appendGroupSystemMessage(c, g.GetId(), uint64(operatorUserID), "group_created", fmt.Sprintf("用户#%d 创建了群「%s」", operatorUserID, g.GetName()), uuid.NewString())
	for _, uid := range memberIDs {
		s.pushGroupEvent(c, uint(uid), "group_created", g.GetId(), uint64(operatorUserID), map[string]interface{}{
			"name":          g.GetName(),
			"owner_user_id": g.GetOwnerUserId(),
			"notice":        g.GetNotice(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"group": gin.H{
			"id":            g.GetId(),
			"name":          g.GetName(),
			"owner_user_id": g.GetOwnerUserId(),
			"notice":        g.GetNotice(),
			"created_at":    g.GetCreatedAt(),
			"updated_at":    g.GetUpdatedAt(),
		},
	})
}

// handleListMyGroups 查询当前登录用户加入的群列表。
func (s *HTTPServer) handleListMyGroups(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	resp, err := s.groupClient.ListUserGroups(ctxWithTrace(c), &pbgroup.ListUserGroupsRequest{
		UserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	groups := make([]gin.H, 0, len(resp.GetGroups()))
	for _, g := range resp.GetGroups() {
		groups = append(groups, gin.H{
			"id":            g.GetId(),
			"name":          g.GetName(),
			"owner_user_id": g.GetOwnerUserId(),
			"created_at":    g.GetCreatedAt(),
			"updated_at":    g.GetUpdatedAt(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"groups": groups})
}

// handleListGroupConversations 查询当前用户群会话（含未读）。
func (s *HTTPServer) handleListGroupConversations(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	resp, err := s.groupClient.ListUserGroupConversations(ctxWithTrace(c), &pbgroup.ListUserGroupConversationsRequest{
		UserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.Unimplemented:
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "group-service version mismatch: missing ListUserGroupConversations, please restart/rebuild group-service"})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		g := it.GetGroup()
		items = append(items, gin.H{
			"group": gin.H{
				"id":            g.GetId(),
				"name":          g.GetName(),
				"owner_user_id": g.GetOwnerUserId(),
				"notice":        g.GetNotice(),
				"created_at":    g.GetCreatedAt(),
				"updated_at":    g.GetUpdatedAt(),
			},
			"last_seq":     it.GetLastSeq(),
			"read_seq":     it.GetReadSeq(),
			"unread_count": it.GetUnreadCount(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// handleGetGroup 查询群详情（成员可见）。
func (s *HTTPServer) handleGetGroup(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	resp, err := s.groupClient.GetGroup(ctxWithTrace(c), &pbgroup.GetGroupRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	g := resp.GetGroup()
	c.JSON(http.StatusOK, gin.H{"group": gin.H{
		"id":            g.GetId(),
		"name":          g.GetName(),
		"owner_user_id": g.GetOwnerUserId(),
		"notice":        g.GetNotice(),
		"created_at":    g.GetCreatedAt(),
		"updated_at":    g.GetUpdatedAt(),
	}})
}

// handleUpdateGroup 更新群资料（仅群主）。
func (s *HTTPServer) handleUpdateGroup(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var req gatewaymodel.UpdateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	resp, err := s.groupClient.UpdateGroup(ctxWithTrace(c), &pbgroup.UpdateGroupRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(userID),
		Name:           req.Name,
		Notice:         req.Notice,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	g := resp.GetGroup()
	memberIDs := s.listGroupMemberIDs(c, g.GetId())
	s.appendGroupSystemMessage(c, g.GetId(), uint64(userID), "group_updated", fmt.Sprintf("用户#%d 更新了群资料", userID), uuid.NewString())
	for _, uid := range memberIDs {
		s.pushGroupEvent(c, uint(uid), "group_updated", g.GetId(), uint64(userID), map[string]interface{}{
			"name":          g.GetName(),
			"owner_user_id": g.GetOwnerUserId(),
			"notice":        g.GetNotice(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"group": gin.H{
		"id":            g.GetId(),
		"name":          g.GetName(),
		"owner_user_id": g.GetOwnerUserId(),
		"notice":        g.GetNotice(),
		"created_at":    g.GetCreatedAt(),
		"updated_at":    g.GetUpdatedAt(),
	}})
}

// handleLeaveGroup 退出群。
func (s *HTTPServer) handleLeaveGroup(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	// 退出前先拿成员快照，用于事件推送。
	memberIDsBefore := s.listGroupMemberIDs(c, groupID)
	// 在退群前写系统消息，保证操作人仍具备群成员权限，历史可回放。
	s.appendGroupSystemMessage(c, groupID, uint64(userID), "group_member_left", fmt.Sprintf("用户#%d 退出了群", userID), uuid.NewString())
	resp, err := s.groupClient.LeaveGroup(ctxWithTrace(c), &pbgroup.LeaveGroupRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition:
				c.JSON(http.StatusConflict, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, uid := range memberIDsBefore {
		s.pushGroupEvent(c, uint(uid), "group_member_left", groupID, uint64(userID), map[string]interface{}{
			"user_id": userID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

// handleDisbandGroup 解散群（仅群主）。
func (s *HTTPServer) handleDisbandGroup(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	// 解散前先拿成员快照，解散后无法再查。
	memberIDsBefore := s.listGroupMemberIDs(c, groupID)
	resp, err := s.groupClient.DisbandGroup(ctxWithTrace(c), &pbgroup.DisbandGroupRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, uid := range memberIDsBefore {
		s.pushGroupEvent(c, uint(uid), "group_disbanded", groupID, uint64(userID), nil)
	}
	// 解散后群已删除，无法再写系统消息，因此仅做实时事件。
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

// handleTransferGroupOwner 转让群主。
func (s *HTTPServer) handleTransferGroupOwner(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var req gatewaymodel.TransferGroupOwnerRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TargetUserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	resp, err := s.groupClient.TransferOwner(ctxWithTrace(c), &pbgroup.TransferOwnerRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(userID),
		TargetUserId:   req.TargetUserID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition:
				c.JSON(http.StatusConflict, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	g := resp.GetGroup()
	memberIDs := s.listGroupMemberIDs(c, g.GetId())
	s.appendGroupSystemMessage(c, g.GetId(), uint64(userID), "group_owner_transferred", fmt.Sprintf("群主已从用户#%d 转让给 用户#%d", userID, req.TargetUserID), uuid.NewString())
	for _, uid := range memberIDs {
		s.pushGroupEvent(c, uint(uid), "group_owner_transferred", g.GetId(), uint64(userID), map[string]interface{}{
			"new_owner_user_id": g.GetOwnerUserId(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"group": gin.H{
		"id":            g.GetId(),
		"name":          g.GetName(),
		"owner_user_id": g.GetOwnerUserId(),
		"notice":        g.GetNotice(),
		"created_at":    g.GetCreatedAt(),
		"updated_at":    g.GetUpdatedAt(),
	}})
}

// handleAddGroupMember 添加群成员。
func (s *HTTPServer) handleAddGroupMember(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	operatorUserID := userIDVal.(uint)
	// 非法请求	 group id 不能为空
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	// 非法请求  target user id 不能为空
	var req gatewaymodel.AddGroupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TargetUserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	// 添加群成员
	resp, err := s.groupClient.AddMember(ctxWithTrace(c), &pbgroup.AddMemberRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(operatorUserID),
		TargetUserId:   req.TargetUserID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition:
				c.JSON(http.StatusConflict, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
	// 实时事件：通知新成员和现有成员。
	memberIDs := s.listGroupMemberIDs(c, groupID)
	s.appendGroupSystemMessage(c, groupID, uint64(operatorUserID), "group_member_added", fmt.Sprintf("用户#%d 邀请 用户#%d 加入群", operatorUserID, req.TargetUserID), uuid.NewString())
	for _, uid := range memberIDs {
		s.pushGroupEvent(c, uint(uid), "group_member_added", groupID, uint64(operatorUserID), map[string]interface{}{
			"target_user_id": req.TargetUserID,
		})
	}
}

// handleRemoveGroupMember 移除群成员。
func (s *HTTPServer) handleRemoveGroupMember(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	operatorUserID := userIDVal.(uint)
	// 非法请求  group id 不能为空
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	// 非法请求  target user id 不能为空
	targetID, err := strconv.ParseUint(c.Param("user_id"), 10, 64)
	if err != nil || targetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	// 移除群成员
	memberIDsBefore := s.listGroupMemberIDs(c, groupID)
	resp, err := s.groupClient.RemoveMember(ctxWithTrace(c), &pbgroup.RemoveMemberRequest{
		GroupId:        groupID,
		OperatorUserId: uint64(operatorUserID),
		TargetUserId:   targetID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition:
				c.JSON(http.StatusConflict, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 返回结果
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
	s.appendGroupSystemMessage(c, groupID, uint64(operatorUserID), "group_member_removed", fmt.Sprintf("用户#%d 将 用户#%d 移出群", operatorUserID, targetID), uuid.NewString())
	for _, uid := range memberIDsBefore {
		s.pushGroupEvent(c, uint(uid), "group_member_removed", groupID, uint64(operatorUserID), map[string]interface{}{
			"target_user_id": targetID,
		})
	}
}

// handleListGroupMembers 查询群成员列表。
func (s *HTTPServer) handleListGroupMembers(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	// 非法请求  group id 不能为空
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	// 权限校验：仅群成员可查看成员列表。
	memberResp, err := s.groupClient.IsMember(ctxWithTrace(c), &pbgroup.IsMemberRequest{
		GroupId: groupID,
		UserId:  uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		log.Printf("[trace=%s] list group members is_member check failed: uid=%d gid=%d err=%v", c.GetString("trace_id"), userID, groupID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !memberResp.GetIsMember() {
		log.Printf("[trace=%s] list group members denied: uid=%d gid=%d not member", c.GetString("trace_id"), userID, groupID)
		c.JSON(http.StatusForbidden, gin.H{"error": "user is not group member"})
		return
	}
	// 查询群成员
	resp, err := s.groupClient.ListMembers(ctxWithTrace(c), &pbgroup.ListMembersRequest{
		GroupId: groupID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			}
		}
		log.Printf("[trace=%s] list group members failed: uid=%d gid=%d err=%v", c.GetString("trace_id"), userID, groupID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 转换为 gin.H 对象
	list := make([]gin.H, 0, len(resp.GetMembers()))
	for _, m := range resp.GetMembers() {
		list = append(list, gin.H{
			"id":        m.GetId(),
			"group_id":  m.GetGroupId(),
			"user_id":   m.GetUserId(),
			"role":      m.GetRole(),
			"joined_at": m.GetJoinedAt(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"members": list})
}

// handleSendGroupMessage 发送群消息：鉴权用户 -> 校验成员 -> 写入 Kafka group-message。
func (s *HTTPServer) handleSendGroupMessage(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	fromUserID := userIDVal.(uint)
	// 非法请求  group id 不能为空
	groupID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	groupID := uint(groupID64)
	// 非法请求  content 不能为空
	var req gatewaymodel.SendGroupMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message content is empty"})
		return
	}
	if len(req.Content) > 4000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message content too long"})
		return
	}
	if req.ClientMsgID == "" {
		req.ClientMsgID = uuid.NewString()
	}
	// 轻量防刷：按 用户+群 每秒最多 20 条。
	if s.redisClient != nil {
		sec := time.Now().Unix()
		key := fmt.Sprintf("rate:group:send:%d:%d:%d", fromUserID, groupID, sec)
		n, err := s.redisClient.Incr(c.Request.Context(), key).Result()
		if err == nil {
			if n == 1 {
				_ = s.redisClient.Expire(c.Request.Context(), key, 2*time.Second).Err()
			}
			if n > 20 {
				log.Printf("[trace=%s] send group message rate limited: uid=%d gid=%d", c.GetString("trace_id"), fromUserID, groupID)
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "group message rate limited"})
				return
			}
		}
	}

	// 先做成员校验，避免无效消息进入 Kafka。
	memberResp, err := s.groupClient.IsMember(ctxWithTrace(c), &pbgroup.IsMemberRequest{
		GroupId: uint64(groupID),
		UserId:  uint64(fromUserID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		log.Printf("[trace=%s] send group message is_member check failed: uid=%d gid=%d err=%v", c.GetString("trace_id"), fromUserID, groupID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !memberResp.GetIsMember() {
		log.Printf("[trace=%s] send group message denied: uid=%d gid=%d not member", c.GetString("trace_id"), fromUserID, groupID)
		c.JSON(http.StatusForbidden, gin.H{"error": "user is not group member"})
		return
	}
	// 转换为 gin.H 对象
	traceID := c.GetString("trace_id")
	evt := gin.H{
		"trace_id": traceID,
		"event_id": req.ClientMsgID,
		"group_id": groupID,
		"from":     fromUserID,
		"content":  req.Content,
	}
	// 转换为 json 数据
	data, err := json.Marshal(evt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal group message failed"})
		return
	}
	// 发送群消息 写入 Kafka group-message
	if err := s.kafkaProducer.SendMessage(context.Background(), "group-message", "", data); err != nil {
		log.Printf("[trace=%s] send group message kafka failed: uid=%d gid=%d event=%s err=%v", c.GetString("trace_id"), fromUserID, groupID, req.ClientMsgID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "group message queued", req.ClientMsgID, map[string]interface{}{"group_id": uint64(groupID)})
	// 返回结果
	c.JSON(http.StatusOK, gin.H{"message": "group message queued"})
}

// handleListGroupMessages 查询当前用户可见的群消息历史。
func (s *HTTPServer) handleListGroupMessages(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	limit := int32(50)
	if s := c.Query("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 200 {
			limit = int32(n)
		}
	}
	var beforeSeq uint64
	if s := c.Query("before_seq"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || n == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid before_seq"})
			return
		}
		beforeSeq = n
	}
	resp, err := s.groupClient.ListGroupMessages(ctxWithTrace(c), &pbgroup.ListGroupMessagesRequest{
		GroupId:   groupID64,
		UserId:    uint64(userID),
		Limit:     limit,
		BeforeSeq: beforeSeq,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			}
		}
		log.Printf("[trace=%s] list group messages failed: uid=%d gid=%d err=%v", c.GetString("trace_id"), userID, groupID64, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	list := make([]gin.H, 0, len(resp.GetMessages()))
	for _, m := range resp.GetMessages() {
		list = append(list, gin.H{
			"id":           m.GetId(),
			"group_id":     m.GetGroupId(),
			"from_user_id": m.GetFromUserId(),
			"message_type": m.GetMessageType(),
			"content":      m.GetContent(),
			"seq":          m.GetSeq(),
			"created_at":   m.GetCreatedAt(),
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"messages":        list,
		"has_more":        resp.GetHasMore(),
		"next_before_seq": resp.GetNextBeforeSeq(),
	})
}

// handleMarkGroupRead 上报群已读游标。
func (s *HTTPServer) handleMarkGroupRead(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	groupID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var req struct {
		ReadSeq uint64 `json:"read_seq"`
	}
	_ = c.ShouldBindJSON(&req)
	resp, err := s.groupClient.MarkGroupRead(ctxWithTrace(c), &pbgroup.MarkGroupReadRequest{
		GroupId: groupID64,
		UserId:  uint64(userID),
		ReadSeq: req.ReadSeq,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			case codes.Unimplemented:
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "group-service version mismatch: missing MarkGroupRead, please restart/rebuild group-service"})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"read_seq": resp.GetReadSeq()})
}

// handleFilePrepare 申请文件上传。
func (s *HTTPServer) handleFilePrepare(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	var req gatewaymodel.FilePrepareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	resp, err := s.fileClient.PrepareUpload(ctxWithTrace(c), &pbfile.PrepareUploadRequest{
		OwnerUserId:    uint64(userID),
		ClientUploadId: req.ClientUploadID,
		FileName:       req.FileName,
		FileSize:       req.FileSize,
		MimeType:       req.MimeType,
		BizType:        req.BizType,
		PeerId:         req.PeerID,
		GroupId:        req.GroupID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "file prepare success", req.ClientUploadID, nil)
	c.JSON(http.StatusOK, gin.H{"file": mapPBFile(resp.GetFile())})
}

// handleFileCommit 确认文件上传完成。
func (s *HTTPServer) handleFileCommit(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	fileID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || fileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
		return
	}
	var req gatewaymodel.FileCommitRequest
	_ = c.ShouldBindJSON(&req)
	resp, err := s.fileClient.CommitUpload(ctxWithTrace(c), &pbfile.CommitUploadRequest{
		FileId:      fileID,
		OwnerUserId: uint64(userID),
		Etag:        req.ETag,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition:
				c.JSON(http.StatusConflict, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "file commit success", strconv.FormatUint(fileID, 10), nil)
	c.JSON(http.StatusOK, gin.H{"file": mapPBFile(resp.GetFile())})
}

// handleFileGet 查询文件信息/下载地址。
func (s *HTTPServer) handleFileGet(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	userID := userIDVal.(uint)
	fileID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || fileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
		return
	}
	resp, err := s.fileClient.GetFile(ctxWithTrace(c), &pbfile.GetFileRequest{
		FileId:         fileID,
		OperatorUserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "file get success", strconv.FormatUint(fileID, 10), nil)
	c.JSON(http.StatusOK, gin.H{"file": mapPBFile(resp.GetFile())})
}

func mapPBFile(f *pbfile.FileItem) gin.H {
	if f == nil {
		return gin.H{}
	}
	return gin.H{
		"id":            f.GetId(),
		"owner_user_id": f.GetOwnerUserId(),
		"biz_type":      f.GetBizType(),
		"peer_id":       f.GetPeerId(),
		"group_id":      f.GetGroupId(),
		"object_key":    f.GetObjectKey(),
		"file_name":     f.GetFileName(),
		"file_size":     f.GetFileSize(),
		"mime_type":     f.GetMimeType(),
		"status":        f.GetStatus(),
		"reject_reason": f.GetRejectReason(),
		"upload_url":    f.GetUploadUrl(),
		"download_url":  f.GetDownloadUrl(),
		"created_at":    f.GetCreatedAt(),
		"updated_at":    f.GetUpdatedAt(),
	}
}

// handleLogsByTrace 查询 trace_id 对应日志。
func (s *HTTPServer) handleLogsByTrace(c *gin.Context) {
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trace_id is required"})
		return
	}
	size := strings.TrimSpace(c.Query("size"))
	u := fmt.Sprintf("%s/api/v1/logs/trace/%s", s.logServiceBaseURL, url.PathEscape(traceID))
	if size != "" {
		u += "?size=" + url.QueryEscape(size)
	}
	s.proxyLogQuery(c, u)
}

// handleLogsByEvent 查询 event_id 对应日志。
func (s *HTTPServer) handleLogsByEvent(c *gin.Context) {
	eventID := strings.TrimSpace(c.Query("event_id"))
	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}
	q := url.Values{}
	q.Set("event_id", eventID)
	if size := strings.TrimSpace(c.Query("size")); size != "" {
		q.Set("size", size)
	}
	u := fmt.Sprintf("%s/api/v1/logs/search?%s", s.logServiceBaseURL, q.Encode())
	s.proxyLogQuery(c, u)
}

// handleLogsFilter 按 service/level/time-range 查询日志。
func (s *HTTPServer) handleLogsFilter(c *gin.Context) {
	q := url.Values{}
	if v := strings.TrimSpace(c.Query("service")); v != "" {
		q.Set("service", v)
	}
	if v := strings.TrimSpace(c.Query("level")); v != "" {
		q.Set("level", v)
	}
	if v := strings.TrimSpace(c.Query("start")); v != "" {
		q.Set("start", v)
	}
	if v := strings.TrimSpace(c.Query("end")); v != "" {
		q.Set("end", v)
	}
	if v := strings.TrimSpace(c.Query("size")); v != "" {
		q.Set("size", v)
	}
	u := fmt.Sprintf("%s/api/v1/logs/filter?%s", s.logServiceBaseURL, q.Encode())
	s.proxyLogQuery(c, u)
}

func (s *HTTPServer) proxyLogQuery(c *gin.Context, target string) {
	resp, err := http.Get(target)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log-service unavailable"})
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read log-service response failed"})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var out map[string]interface{}
		if json.Unmarshal(b, &out) == nil {
			c.JSON(resp.StatusCode, out)
			return
		}
		c.JSON(resp.StatusCode, gin.H{"error": string(b)})
		return
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid log-service response"})
		return
	}
	c.JSON(http.StatusOK, out)
}

// handleFileScanDLQList 代理 file-service 的死信任务列表。
func (s *HTTPServer) handleFileScanDLQList(c *gin.Context) {
	u := fmt.Sprintf("%s/api/v1/admin/file-scan/dlq?%s", s.fileServiceBaseURL, c.Request.URL.RawQuery)
	s.proxyFileServiceHTTP(c, http.MethodGet, u)
}

// handleFileScanDLQReplay 代理 file-service 的死信重放。
func (s *HTTPServer) handleFileScanDLQReplay(c *gin.Context) {
	id := strings.TrimSpace(c.Param("file_id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_id is required"})
		return
	}
	u := fmt.Sprintf("%s/api/v1/admin/file-scan/dlq/%s/replay", s.fileServiceBaseURL, url.PathEscape(id))
	s.proxyFileServiceHTTP(c, http.MethodPost, u)
}

func (s *HTTPServer) proxyFileServiceHTTP(c *gin.Context, method, target string) {
	req, err := http.NewRequestWithContext(c.Request.Context(), method, target, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "build file-service request failed"})
		return
	}
	if tid := c.GetHeader("X-Trace-Id"); tid != "" {
		req.Header.Set("X-Trace-Id", tid)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "file-service unavailable"})
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read file-service response failed"})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var out map[string]interface{}
		if json.Unmarshal(b, &out) == nil {
			c.JSON(resp.StatusCode, out)
			return
		}
		c.JSON(resp.StatusCode, gin.H{"error": string(b)})
		return
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		c.JSON(http.StatusOK, gin.H{"raw": string(b)})
		return
	}
	c.JSON(http.StatusOK, out)
}

// handleAdminHealth 聚合返回各后端服务健康状态，供管理后台展示。
func (s *HTTPServer) handleAdminHealth(c *gin.Context) {
	type svc struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		URL       string `json:"url"`
		Status    string `json:"status"`
		LatencyMS int64  `json:"latency_ms"`
		Error     string `json:"error,omitempty"`
	}
	targets := []svc{
		{ID: "gateway", Name: "Gateway", URL: "http://localhost:8080/health"},
		{ID: "auth", Name: "Auth Service", URL: "http://localhost:9000/health"},
		{ID: "user", Name: "User Service", URL: "http://localhost:9001/health"},
		{ID: "friend", Name: "Friend Service", URL: "http://localhost:9002/health"},
		{ID: "conversation", Name: "Conversation Service", URL: "http://localhost:9003/health"},
		{ID: "group", Name: "Group Service", URL: "http://localhost:9004/health"},
		{ID: "file", Name: "File Service", URL: s.fileServiceBaseURL + "/health"},
		{ID: "log", Name: "Log Service", URL: s.logServiceBaseURL + "/health"},
	}
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	for i := range targets {
		start := time.Now()
		resp, err := client.Get(targets[i].URL)
		targets[i].LatencyMS = time.Since(start).Milliseconds()
		if err != nil {
			targets[i].Status = "down"
			targets[i].Error = err.Error()
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			targets[i].Status = "up"
		} else {
			targets[i].Status = "down"
			targets[i].Error = fmt.Sprintf("http %d", resp.StatusCode)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"generated_at": time.Now().Format(time.RFC3339),
		"services":     targets,
	})
}

// handleAdminMetrics 返回后台运行概览指标（在线连接 + 日志统计）。
func (s *HTTPServer) handleAdminMetrics(c *gin.Context) {
	online := int64(0)
	if s.redisClient != nil {
		var cursor uint64
		for {
			keys, next, err := s.redisClient.Scan(c.Request.Context(), cursor, "ws:conn:*", 500).Result()
			if err != nil {
				break
			}
			online += int64(len(keys))
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}

	metricsURL := strings.TrimRight(s.logServiceBaseURL, "/") + "/api/v1/admin/metrics"
	resp, err := http.Get(metricsURL)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":          "log-service metrics unavailable",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "read metrics response failed",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":          "log-service metrics request failed",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(body, &out); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "invalid log-service metrics response",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	out["online_users"] = online
	out["log_service_ok"] = true
	out["generated_at"] = time.Now().Format(time.RFC3339)
	c.JSON(http.StatusOK, out)
}

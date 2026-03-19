package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

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
	authGroup.GET("/friends", s.handleListFriends)
	authGroup.GET("/conversations", s.handleListConversations)
	authGroup.GET("/messages", s.handleListMessages)
	authGroup.PUT("/conversations/:peer_id/read", s.handleConversationRead)
	authGroup.POST("/friends/requests", s.handleSendFriendRequest)
	authGroup.POST("/friends/requests/:id/approve", s.handleApproveFriendRequest)
	authGroup.POST("/friends/requests/:id/reject", s.handleRejectFriendRequest)
	authGroup.POST("/friends/blacklist/:user_id", s.handleBlockUser)
	authGroup.GET("/friends/requests/incoming", s.handleListIncomingFriendRequests)
	authGroup.GET("/friends/requests/outgoing", s.handleListOutgoingFriendRequests)
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

// handleBlockUser 拉黑用户。
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
	// 拉黑用户
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

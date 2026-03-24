package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	conversationhandler "pim/internal/conversation/handler"
	pbfriend "pim/internal/friend/pb"
	gatewaymodel "pim/internal/gateway/model"
	gatewayservice "pim/internal/gateway/service"
)

// handleListFriends 查询好友列表。
func (s *HTTPServer) handleListFriends(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
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

// handleSendFriendRequest 发送好友申请。
func (s *HTTPServer) handleSendFriendRequest(c *gin.Context) {
	fromUserID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req gatewaymodel.SendFriendRequestRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ToUserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	resp, err := s.friendClient.SendFriendRequest(ctxWithTrace(c), &pbfriend.SendFriendRequestRequest{
		FromUserId: uint64(fromUserID),
		ToUserId:   uint64(req.ToUserID),
		Remark:     req.Remark,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 申请落库成功后，尝试实时通知对方刷新申请列表。
	s.pushFriendRequestEvent(c, req.ToUserID, "friend_request_created", resp.GetRequestId(), uint64(fromUserID), uint64(req.ToUserID), resp.GetStatus(), req.Remark)
	c.JSON(http.StatusOK, gin.H{"request_id": resp.GetRequestId(), "status": resp.GetStatus()})
}

func (s *HTTPServer) handleApproveFriendRequest(c *gin.Context) {
	operatorUserID, ok := requireUserID(c)
	if !ok {
		return
	}
	reqID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || reqID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request id"})
		return
	}
	detail, err := s.friendClient.GetFriendRequestByID(ctxWithTrace(c), &pbfriend.GetFriendRequestByIDRequest{RequestId: reqID})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	item := detail.GetItem()
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "friend request not found"})
		return
	}
	// 先查详情再审批，确保后续推送能拿到 from/to 用户。
	resp, err := s.friendClient.ApproveFriendRequest(ctxWithTrace(c), &pbfriend.ApproveFriendRequestRequest{RequestId: reqID, OperatorUserId: uint64(operatorUserID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fromID, toID := item.GetFromUserId(), item.GetToUserId()
	s.pushFriendRequestEvent(c, uint(toID), "friend_request_updated", reqID, fromID, toID, "accepted", item.GetRemark())
	s.pushFriendRequestEvent(c, uint(fromID), "friend_request_updated", reqID, fromID, toID, "accepted", item.GetRemark())
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

func (s *HTTPServer) handleRejectFriendRequest(c *gin.Context) {
	operatorUserID, ok := requireUserID(c)
	if !ok {
		return
	}
	reqID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || reqID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request id"})
		return
	}
	detail, err := s.friendClient.GetFriendRequestByID(ctxWithTrace(c), &pbfriend.GetFriendRequestByIDRequest{RequestId: reqID})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	item := detail.GetItem()
	if item == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "friend request not found"})
		return
	}
	// 先查详情再拒绝，避免拒绝后无法确定通知对象。
	resp, err := s.friendClient.RejectFriendRequest(ctxWithTrace(c), &pbfriend.RejectFriendRequestRequest{RequestId: reqID, OperatorUserId: uint64(operatorUserID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fromID, toID := item.GetFromUserId(), item.GetToUserId()
	s.pushFriendRequestEvent(c, uint(toID), "friend_request_updated", reqID, fromID, toID, "rejected", item.GetRemark())
	s.pushFriendRequestEvent(c, uint(fromID), "friend_request_updated", reqID, fromID, toID, "rejected", item.GetRemark())
	c.JSON(http.StatusOK, gin.H{"message": resp.GetMessage()})
}

func (s *HTTPServer) handleDeleteFriend(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	targetID, err := strconv.ParseUint(c.Param("user_id"), 10, 64)
	if err != nil || targetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}
	// 复用 friend-service 现有 BlockUser RPC，语义已调整为“双向删除好友”。
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

func (s *HTTPServer) handleListIncomingFriendRequests(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var q gatewaymodel.ListFriendRequestsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	resp, err := s.friendClient.ListIncomingFriendRequests(ctxWithTrace(c), &pbfriend.ListIncomingFriendRequestsRequest{UserId: uint64(userID), Status: q.Status, Cursor: q.Cursor, Limit: q.Limit})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, gin.H{"request_id": it.GetRequestId(), "from_user_id": it.GetFromUserId(), "to_user_id": it.GetToUserId(), "status": it.GetStatus(), "remark": it.GetRemark(), "created_at": it.GetCreatedAt(), "updated_at": it.GetUpdatedAt()})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": resp.GetNextCursor(), "has_more": resp.GetHasMore()})
}

func (s *HTTPServer) handleListOutgoingFriendRequests(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var q gatewaymodel.ListFriendRequestsQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid query"})
		return
	}
	resp, err := s.friendClient.ListOutgoingFriendRequests(ctxWithTrace(c), &pbfriend.ListOutgoingFriendRequestsRequest{UserId: uint64(userID), Status: q.Status, Cursor: q.Cursor, Limit: q.Limit})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, gin.H{"request_id": it.GetRequestId(), "from_user_id": it.GetFromUserId(), "to_user_id": it.GetToUserId(), "status": it.GetStatus(), "remark": it.GetRemark(), "created_at": it.GetCreatedAt(), "updated_at": it.GetUpdatedAt()})
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": resp.GetNextCursor(), "has_more": resp.GetHasMore()})
}

// pushFriendRequestEvent 仅做实时通知，失败不影响主流程。
func (s *HTTPServer) pushFriendRequestEvent(c *gin.Context, toUserID uint, eventType string, requestID uint64, fromUserID uint64, targetUserID uint64, status string, remark string) {
	payload, _ := json.Marshal(gin.H{"type": eventType, "request_id": requestID, "from_user_id": fromUserID, "to_user_id": targetUserID, "status": status, "remark": remark, "ts": time.Now().Unix()})
	if err := conversationhandler.PushToUser(toUserID, 0, string(payload)); err != nil {
		log.Printf("[trace=%v] push friend event failed to user=%d: %v", c.GetString("trace_id"), toUserID, err)
	}
}

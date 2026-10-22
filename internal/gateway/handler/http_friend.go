package handler

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbfriend "pim/internal/friend/pb"
	gatewaymodel "pim/internal/gateway/model"
	gatewayservice "pim/internal/gateway/service"
	pbuser "pim/internal/user/pb"
)

// handleListFriends 查询好友列表。
func (s *HTTPServer) handleListFriends(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	ctx := ctxWithTrace(c)
	resp, err := s.friendClient.ListFriends(ctx, &pbfriend.ListFriendsRequest{UserId: uint64(userID)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	list := make([]gin.H, len(resp.Friends))
	var wg sync.WaitGroup
	for i, f := range resp.Friends {
		item := gatewayservice.FriendFromPB(f)
		list[i] = item
		fid := f.GetFriendId()
		if fid == 0 || s.userClient == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, friendID uint64) {
			defer wg.Done()
			s.attachUserProfile(ctx, list[idx], friendID)
		}(i, fid)
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"friends": list})
}

// attachUserProfile 为列表项补充 username/nickname（失败则保持原样）。
func (s *HTTPServer) attachUserProfile(ctx context.Context, item gin.H, userID uint64) {
	if item == nil || userID == 0 || s.userClient == nil {
		return
	}
	resp, err := s.userClient.GetByID(ctx, &pbuser.GetByIDRequest{UserId: userID})
	if err != nil || resp.GetUser() == nil {
		return
	}
	u := resp.GetUser()
	item["username"] = u.GetUsername()
	item["nickname"] = u.GetNickname()
	item["avatar_url"] = u.GetAvatarUrl()
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
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition, codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			}
		}
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
	// 双方各收到「对方」成为好友的增量事件，避免再靠全量轮询。
	s.pushFriendAdded(c, uint(fromID), toID)
	s.pushFriendAdded(c, uint(toID), fromID)
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
	s.pushUserEventJSON(c, uint(userID), gin.H{"type": "friend_removed", "user_id": targetID})
	s.pushUserEventJSON(c, uint(targetID), gin.H{"type": "friend_removed", "user_id": uint64(userID)})
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
	ctx := ctxWithTrace(c)
	resp, err := s.friendClient.ListIncomingFriendRequests(ctx, &pbfriend.ListIncomingFriendRequestsRequest{UserId: uint64(userID), Status: q.Status, Cursor: q.Cursor, Limit: q.Limit})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, len(resp.GetItems()))
	var wg sync.WaitGroup
	for i, it := range resp.GetItems() {
		item := gin.H{"request_id": it.GetRequestId(), "from_user_id": it.GetFromUserId(), "to_user_id": it.GetToUserId(), "status": it.GetStatus(), "remark": it.GetRemark(), "created_at": it.GetCreatedAt(), "updated_at": it.GetUpdatedAt()}
		items[i] = item
		fromID := it.GetFromUserId()
		if fromID == 0 || s.userClient == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, uid uint64) {
			defer wg.Done()
			s.attachUserProfile(ctx, items[idx], uid)
		}(i, fromID)
	}
	wg.Wait()
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
	ctx := ctxWithTrace(c)
	resp, err := s.friendClient.ListOutgoingFriendRequests(ctx, &pbfriend.ListOutgoingFriendRequestsRequest{UserId: uint64(userID), Status: q.Status, Cursor: q.Cursor, Limit: q.Limit})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, len(resp.GetItems()))
	var wg sync.WaitGroup
	for i, it := range resp.GetItems() {
		item := gin.H{"request_id": it.GetRequestId(), "from_user_id": it.GetFromUserId(), "to_user_id": it.GetToUserId(), "status": it.GetStatus(), "remark": it.GetRemark(), "created_at": it.GetCreatedAt(), "updated_at": it.GetUpdatedAt()}
		items[i] = item
		toID := it.GetToUserId()
		if toID == 0 || s.userClient == nil {
			continue
		}
		wg.Add(1)
		go func(idx int, uid uint64) {
			defer wg.Done()
			s.attachUserProfile(ctx, items[idx], uid)
		}(i, toID)
	}
	wg.Wait()
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": resp.GetNextCursor(), "has_more": resp.GetHasMore()})
}

// pushFriendRequestEvent 仅做实时通知，失败不影响主流程。
func (s *HTTPServer) pushFriendRequestEvent(c *gin.Context, toUserID uint, eventType string, requestID uint64, fromUserID uint64, targetUserID uint64, status string, remark string) {
	s.pushUserEventJSON(c, toUserID, gin.H{
		"type": eventType, "request_id": requestID, "from_user_id": fromUserID, "to_user_id": targetUserID,
		"status": status, "remark": remark,
	})
}

// pushUserEventJSON 向单用户推送 JSON 事件（跨 gateway 经 Redis+PushService）；失败只打日志。
func (s *HTTPServer) pushUserEventJSON(c *gin.Context, toUserID uint, payload gin.H) {
	if toUserID == 0 || payload == nil {
		return
	}
	if _, ok := payload["ts"]; !ok {
		payload["ts"] = time.Now().Unix()
	}
	data, _ := json.Marshal(payload)
	traceID := ""
	ctx := context.Background()
	if c != nil {
		traceID = c.GetString("trace_id")
		ctx = ctxWithTrace(c)
	}
	if err := s.pushContentRouted(ctx, toUserID, 0, string(data)); err != nil {
		log.Printf("[trace=%v] push event failed to user=%d type=%v: %v", traceID, toUserID, payload["type"], err)
	}
}

// pushFriendAdded 通知 toUser：peerUserID 已成为好友（带展示名）。
func (s *HTTPServer) pushFriendAdded(c *gin.Context, toUserID uint, peerUserID uint64) {
	if toUserID == 0 || peerUserID == 0 {
		return
	}
	payload := gin.H{"type": "friend_added", "user_id": peerUserID}
	if s.userClient != nil {
		ctx := context.Background()
		if c != nil {
			ctx = ctxWithTrace(c)
		}
		if resp, err := s.userClient.GetByID(ctx, &pbuser.GetByIDRequest{UserId: peerUserID}); err == nil && resp.GetUser() != nil {
			u := resp.GetUser()
			payload["username"] = u.GetUsername()
			payload["nickname"] = u.GetNickname()
			payload["avatar_url"] = u.GetAvatarUrl()
		}
	}
	s.pushUserEventJSON(c, toUserID, payload)
}

// fanoutProfileUpdated 把资料变更推给所有好友（异步调用方传入 background 安全的 trace）。
func (s *HTTPServer) fanoutProfileUpdated(traceID string, me uint, u *pbuser.User) {
	if me == 0 || u == nil || s.friendClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	resp, err := s.friendClient.ListFriends(ctx, &pbfriend.ListFriendsRequest{UserId: uint64(me)})
	if err != nil {
		log.Printf("[trace=%s] fanout profile_updated list friends failed user=%d: %v", traceID, me, err)
		return
	}
	payload := gin.H{
		"type":       "profile_updated",
		"user_id":    u.GetId(),
		"nickname":   u.GetNickname(),
		"username":   u.GetUsername(),
		"avatar_url": u.GetAvatarUrl(),
		"ts":         time.Now().Unix(),
	}
	data, _ := json.Marshal(payload)
	for _, f := range resp.GetFriends() {
		fid := f.GetFriendId()
		if fid == 0 {
			continue
		}
		if err := s.pushContentRouted(ctx, uint(fid), me, string(data)); err != nil {
			log.Printf("[trace=%s] push profile_updated failed to user=%d: %v", traceID, fid, err)
		}
	}
}

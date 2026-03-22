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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	conversationhandler "pim/internal/conversation/handler"
	gatewaymodel "pim/internal/gateway/model"
	pbgroup "pim/internal/group/pb"
)

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
	var req gatewaymodel.CreateGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
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
	g := resp.GetGroup()
	// 创建完成后立即查询成员快照，保证推送范围与当前群成员一致。
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
	// 退群后可能无法再枚举到本人，先拍快照用于后续事件扇出。
	memberIDsBefore := s.listGroupMemberIDs(c, groupID)
	// 在退群前写系统消息，避免退群后因权限变化导致写入失败。
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
	// 解散后群不存在，必须先缓存成员列表再推送事件。
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
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	var req gatewaymodel.AddGroupMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TargetUserID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
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
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	targetID, err := strconv.ParseUint(c.Param("user_id"), 10, 64)
	if err != nil || targetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
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
	groupID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
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

// handleSendGroupMessage 发送群消息：鉴权用户 -> 校验成员 -> 写入 Kafka。
func (s *HTTPServer) handleSendGroupMessage(c *gin.Context) {
	userIDVal, _ := c.Get("userID")
	fromUserID := userIDVal.(uint)
	groupID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	groupID := uint(groupID64)
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
	// 入 Kafka 前先做成员校验，避免非法消息进入异步链路。
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
	evt := gin.H{
		"trace_id": c.GetString("trace_id"),
		"event_id": req.ClientMsgID,
		"group_id": groupID,
		"from":     fromUserID,
		"content":  req.Content,
	}
	// 群消息统一走 group-message topic，由 group consumer 负责落库+扇出。
	data, err := json.Marshal(evt)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal group message failed"})
		return
	}
	if err := s.kafkaProducer.SendMessage(context.Background(), "group-message", "", data); err != nil {
		log.Printf("[trace=%s] send group message kafka failed: uid=%d gid=%d event=%s err=%v", c.GetString("trace_id"), fromUserID, groupID, req.ClientMsgID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "group message queued", req.ClientMsgID, map[string]interface{}{"group_id": uint64(groupID)})
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

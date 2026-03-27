package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"pim/internal/config"
	conversationhandler "pim/internal/conversation/handler"
	gatewaymodel "pim/internal/gateway/model"
	groupmodel "pim/internal/group/model"
	pbgroup "pim/internal/group/pb"
	observemetrics "pim/internal/kit/observability/metrics"
)

type groupReadyCacheEntry struct {
	expiresAt int64
}

type groupMemberLocalCacheEntry struct {
	expiresAt     int64
	version       int64
	authoritative bool
	bitmap        *roaring64.Bitmap
}

type groupMemberNegativeCacheEntry struct {
	expiresAt int64
}

var groupReadyCache sync.Map       // key: groupID(uint64) -> groupReadyCacheEntry
var groupMemberLocalCache sync.Map // key: groupID(uint64) -> groupMemberLocalCacheEntry
var groupMemberNegativeCache sync.Map
var groupMemberLoadGroup singleflight.Group

func groupMembersKey(groupID uint64) string {
	return fmt.Sprintf("group:members:%d", groupID)
}

func groupMembersReadyKey(groupID uint64) string {
	return fmt.Sprintf("group:members:ready:%d", groupID)
}

func isGroupReadyCached(groupID uint64) bool {
	now := time.Now().UnixMilli()
	v, ok := groupReadyCache.Load(groupID)
	if !ok {
		return false
	}
	e := v.(groupReadyCacheEntry)
	if e.expiresAt <= now {
		groupReadyCache.Delete(groupID)
		return false
	}
	return true
}

func markGroupReadyCached(groupID uint64) {
	ttl := int64(config.GatewayGroupReadyCacheTTLms)
	if ttl <= 0 {
		return
	}
	groupReadyCache.Store(groupID, groupReadyCacheEntry{expiresAt: time.Now().UnixMilli() + ttl})
}

func clearGroupReadyCached(groupID uint64) {
	groupReadyCache.Delete(groupID)
}

func loadGroupMembersCached(groupID uint64) (*roaring64.Bitmap, bool) {
	now := time.Now().UnixMilli()
	v, ok := groupMemberLocalCache.Load(groupID)
	if !ok {
		return nil, false
	}
	e := v.(groupMemberLocalCacheEntry)
	if !e.authoritative && e.expiresAt <= now {
		groupMemberLocalCache.Delete(groupID)
		return nil, false
	}
	if e.bitmap == nil {
		return nil, false
	}
	return e.bitmap, true
}

func bitmapFromUserIDs(userIDs []uint64) *roaring64.Bitmap {
	bm := roaring64.New()
	for _, uid := range userIDs {
		bm.Add(uid)
	}
	return bm
}

func storeGroupMembersCached(groupID uint64, bm *roaring64.Bitmap, version int64, authoritative bool) {
	ttl := int64(config.GatewayGroupMemberLocalCacheTTLms)
	if ttl <= 0 || bm == nil || bm.IsEmpty() {
		return
	}
	expiresAt := time.Now().UnixMilli() + ttl
	if authoritative {
		// 权威快照不走短 TTL 过期，避免压测中段回落到 Redis 热路径。
		expiresAt = 1<<62 - 1
	}
	groupMemberLocalCache.Store(groupID, groupMemberLocalCacheEntry{
		expiresAt:     expiresAt,
		version:       version,
		authoritative: authoritative,
		bitmap:        bm,
	})
}

func (s *HTTPServer) loadGroupMembersAuthoritative(ctx context.Context, groupID uint64) bool {
	if s == nil || s.redisClient == nil || groupID == 0 {
		return false
	}
	key := strconv.FormatUint(groupID, 10)
	_, err, _ := groupMemberLoadGroup.Do(key, func() (interface{}, error) {
		if _, ok := loadGroupMembersCached(groupID); ok {
			return true, nil
		}
		ids, err := s.redisClient.SMembers(ctx, groupMembersKey(groupID)).Result()
		if err != nil || len(ids) == 0 {
			return false, err
		}
		userIDs := make([]uint64, 0, len(ids))
		for _, raw := range ids {
			uid, parseErr := strconv.ParseUint(raw, 10, 64)
			if parseErr != nil {
				continue
			}
			userIDs = append(userIDs, uid)
		}
		if len(userIDs) == 0 {
			return false, nil
		}
		storeGroupMembersCached(groupID, bitmapFromUserIDs(userIDs), time.Now().UnixMilli(), true)
		markGroupReadyCached(groupID)
		return true, nil
	})
	if err != nil {
		return false
	}
	if bm, ok := loadGroupMembersCached(groupID); ok && bm != nil {
		return true
	}
	return false
}

func clearGroupMembersCached(groupID uint64) {
	groupMemberLocalCache.Delete(groupID)
}

func groupMemberNegativeKey(groupID uint64, userID uint64) string {
	return fmt.Sprintf("%d:%d", groupID, userID)
}

func loadGroupMemberNegativeCached(groupID uint64, userID uint64) bool {
	v, ok := groupMemberNegativeCache.Load(groupMemberNegativeKey(groupID, userID))
	if !ok {
		return false
	}
	e := v.(groupMemberNegativeCacheEntry)
	if e.expiresAt <= time.Now().UnixMilli() {
		groupMemberNegativeCache.Delete(groupMemberNegativeKey(groupID, userID))
		return false
	}
	return true
}

func storeGroupMemberNegativeCached(groupID uint64, userID uint64) {
	ttl := int64(config.GatewayGroupMemberNegativeCacheTTLms)
	if ttl <= 0 {
		return
	}
	groupMemberNegativeCache.Store(groupMemberNegativeKey(groupID, userID), groupMemberNegativeCacheEntry{
		expiresAt: time.Now().UnixMilli() + ttl,
	})
}

func clearGroupMemberNegativeCached(groupID uint64, userID uint64) {
	groupMemberNegativeCache.Delete(groupMemberNegativeKey(groupID, userID))
}

func applyGroupMemberSyncEvent(groupID uint64, op string, version int64, memberIDs []uint64) {
	if groupID == 0 {
		return
	}
	now := time.Now().UnixMilli()
	if version <= 0 {
		version = now
	}
	if v, ok := groupMemberLocalCache.Load(groupID); ok {
		cur := v.(groupMemberLocalCacheEntry)
		if cur.version > version {
			return
		}
	}
	switch op {
	case "delete":
		clearGroupReadyCached(groupID)
		clearGroupMembersCached(groupID)
	case "snapshot":
		clearGroupReadyCached(groupID)
		storeGroupMembersCached(groupID, bitmapFromUserIDs(memberIDs), version, true)
	case "add":
		cur := roaring64.New()
		if v, ok := groupMemberLocalCache.Load(groupID); ok {
			e := v.(groupMemberLocalCacheEntry)
			if e.bitmap != nil {
				cur = e.bitmap.Clone()
			}
		}
		for _, uid := range memberIDs {
			cur.Add(uid)
		}
		storeGroupMembersCached(groupID, cur, version, true)
	case "remove":
		cur := roaring64.New()
		if v, ok := groupMemberLocalCache.Load(groupID); ok {
			e := v.(groupMemberLocalCacheEntry)
			if e.bitmap != nil {
				cur = e.bitmap.Clone()
			}
		}
		for _, uid := range memberIDs {
			cur.Remove(uid)
		}
		storeGroupMembersCached(groupID, cur, version, true)
	default:
		// 兼容未知 op，忽略。
	}
}

func (s *HTTPServer) emitGroupMemberSyncSnapshot(c *gin.Context, groupID uint64, memberIDs []uint64) {
	if s.kafkaProducer == nil {
		return
	}
	evt := map[string]interface{}{
		"trace_id":        c.GetString("trace_id"),
		"op":              "snapshot",
		"group_id":        groupID,
		"member_user_ids": memberIDs,
		"version":         time.Now().UnixMilli(),
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	if err := s.kafkaProducer.SendMessage(context.Background(), "group-member-sync", strconv.FormatUint(groupID, 10), data); err != nil {
		log.Printf("[trace=%s] emit group-member-sync snapshot failed: gid=%d err=%v", c.GetString("trace_id"), groupID, err)
	}
}

// refreshGroupMemberSyncSnapshot 尝试发布最新成员快照；若快照拉取失败则发布 delete，清理 ready 并强制发送路径回源判定。
func (s *HTTPServer) refreshGroupMemberSyncSnapshot(c *gin.Context, groupID uint64) {
	memberIDs, err := s.listGroupMemberIDsWithErr(c, groupID)
	if err != nil {
		log.Printf("[trace=%s] refresh group-member-sync snapshot failed, fallback delete: gid=%d err=%v", c.GetString("trace_id"), groupID, err)
		clearGroupReadyCached(groupID)
		clearGroupMembersCached(groupID)
		s.emitGroupMemberSyncDelete(c, groupID)
		return
	}
	markGroupReadyCached(groupID)
	clearGroupMembersCached(groupID)
	s.emitGroupMemberSyncSnapshot(c, groupID, memberIDs)
}

func (s *HTTPServer) emitGroupMemberSyncDelete(c *gin.Context, groupID uint64) {
	if s.kafkaProducer == nil {
		return
	}
	evt := map[string]interface{}{
		"trace_id": c.GetString("trace_id"),
		"op":       "delete",
		"group_id": groupID,
		"version":  time.Now().UnixMilli(),
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	if err := s.kafkaProducer.SendMessage(context.Background(), "group-member-sync", strconv.FormatUint(groupID, 10), data); err != nil {
		log.Printf("[trace=%s] emit group-member-sync delete failed: gid=%d err=%v", c.GetString("trace_id"), groupID, err)
	}
	clearGroupReadyCached(groupID)
	clearGroupMembersCached(groupID)
}

func (s *HTTPServer) warmGroupMembersCache(ctx context.Context, groupID uint64) {
	if s.redisClient == nil {
		return
	}
	if _, ok := loadGroupMembersCached(groupID); ok {
		return
	}
	ids, err := s.redisClient.SMembers(ctx, groupMembersKey(groupID)).Result()
	if err != nil || len(ids) == 0 {
		return
	}
	userIDs := make([]uint64, 0, len(ids))
	for _, raw := range ids {
		uid, parseErr := strconv.ParseUint(raw, 10, 64)
		if parseErr != nil {
			continue
		}
		userIDs = append(userIDs, uid)
	}
	if len(userIDs) > 0 {
		storeGroupMembersCached(groupID, bitmapFromUserIDs(userIDs), time.Now().UnixMilli(), true)
	}
}

// prewarmGroupMembersLocalIndex 启动后预热部分活跃群成员索引，降低冷启动阶段 member_check 回源抖动。
// 活跃群来源：Redis 中存在 group:members:ready:* 的群。
func (s *HTTPServer) prewarmGroupMembersLocalIndex(ctx context.Context, limit int) {
	if s == nil || s.redisClient == nil || limit <= 0 {
		return
	}
	start := time.Now()
	var (
		cursor  uint64
		loaded  int
		scanned int
	)
	for loaded < limit {
		keys, next, err := s.redisClient.Scan(ctx, cursor, "group:members:ready:*", 256).Result()
		if err != nil {
			log.Printf("group prewarm scan failed: %v", err)
			break
		}
		for _, k := range keys {
			scanned++
			if loaded >= limit {
				break
			}
			parts := strings.Split(k, ":")
			if len(parts) == 0 {
				continue
			}
			gidStr := parts[len(parts)-1]
			gid, err := strconv.ParseUint(gidStr, 10, 64)
			if err != nil || gid == 0 {
				continue
			}
			if _, ok := loadGroupMembersCached(gid); ok {
				continue
			}
			ids, err := s.redisClient.SMembers(ctx, groupMembersKey(gid)).Result()
			if err != nil || len(ids) == 0 {
				continue
			}
			userIDs := make([]uint64, 0, len(ids))
			for _, raw := range ids {
				uid, parseErr := strconv.ParseUint(raw, 10, 64)
				if parseErr != nil {
					continue
				}
				userIDs = append(userIDs, uid)
			}
			if len(userIDs) == 0 {
				continue
			}
			storeGroupMembersCached(gid, bitmapFromUserIDs(userIDs), time.Now().UnixMilli(), true)
			markGroupReadyCached(gid)
			loaded++
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	log.Printf("group prewarm done: loaded=%d scanned=%d limit=%d elapsed_ms=%d", loaded, scanned, limit, time.Since(start).Milliseconds())
}

// isGroupMemberFast 先查 Redis 成员索引；对 Redis 的 false 判定也做回源复核，避免历史脏索引导致误判 403。
func (s *HTTPServer) isGroupMemberFast(c *gin.Context, groupID uint64, userID uint64) (bool, error) {
	start := time.Now()
	observe := func(stage, result string, began time.Time) {
		observemetrics.ObserveGroupMemberCheckStage(stage, result, time.Since(began).Seconds())
	}
	needFallback := true
	if s.redisClient != nil {
		ctx := c.Request.Context()
		localStart := time.Now()
		if members, ok := loadGroupMembersCached(groupID); ok {
			if members.Contains(userID) {
				observe("local_cache", "hit", localStart)
				observe("total", "hit_local", start)
				return true, nil
			}
			// authoritative 快照未命中，直接判非成员（避免热路径持续回源）。
			if v, ok := groupMemberLocalCache.Load(groupID); ok {
				e := v.(groupMemberLocalCacheEntry)
				if e.authoritative {
					observe("local_cache", "miss_authoritative", localStart)
					observe("total", "miss_authoritative", start)
					return false, nil
				}
			}
			observe("local_cache", "miss_non_authoritative", localStart)
			// 非 authoritative 本地缓存未命中时仍回源兜底。
			needFallback = true
		} else {
			observe("local_cache", "empty", localStart)
			// 本地空时同步 singleflight 拉一次权威快照，避免并发请求全部打 Redis。
			loadStart := time.Now()
			if s.loadGroupMembersAuthoritative(ctx, groupID) {
				if members, ok := loadGroupMembersCached(groupID); ok && members.Contains(userID) {
					observe("local_fill", "hit_after_fill", loadStart)
					observe("total", "hit_local_after_fill", start)
					return true, nil
				}
				if v, ok := groupMemberLocalCache.Load(groupID); ok {
					e := v.(groupMemberLocalCacheEntry)
					if e.authoritative {
						observe("local_fill", "miss_after_fill_authoritative", loadStart)
						observe("total", "miss_after_fill_authoritative", start)
						return false, nil
					}
				}
			} else {
				observe("local_fill", "fill_failed", loadStart)
			}
		}
		redisStart := time.Now()
		ok, err := s.redisClient.SIsMember(ctx, groupMembersKey(groupID), userID).Result()
		if err == nil {
			if ok {
				clearGroupMemberNegativeCached(groupID, userID)
				go s.warmGroupMembersCache(context.Background(), groupID)
				observe("redis_direct", "hit", redisStart)
				observe("total", "hit_redis_direct", start)
				return true, nil
			}
			if loadGroupMemberNegativeCached(groupID, userID) {
				observe("redis_direct", "miss_neg_cache_hit", redisStart)
				observe("total", "miss_neg_cache_hit", start)
				return false, nil
			}
			observe("redis_direct", "miss", redisStart)
			needFallback = true
		} else if err != redis.Nil {
			log.Printf("[trace=%s] sismember failed: gid=%d uid=%d err=%v", c.GetString("trace_id"), groupID, userID, err)
			observe("redis_direct", "error", redisStart)
		}
	}
	if !needFallback {
		observe("total", "no_fallback_false", start)
		return false, nil
	}
	// 回源兜底：不写 ready 标记；仅在确认是成员时补一条 SADD 做自愈。
	fallbackStart := time.Now()
	memberResp, err := s.groupClient.IsMember(ctxWithTrace(c), &pbgroup.IsMemberRequest{
		GroupId: groupID,
		UserId:  userID,
	})
	if err != nil {
		observe("fallback_grpc", "error", fallbackStart)
		observe("total", "fallback_error", start)
		return false, err
	}
	if s.redisClient != nil && memberResp.GetIsMember() {
		_ = s.redisClient.SAdd(c.Request.Context(), groupMembersKey(groupID), userID).Err()
		clearGroupMemberNegativeCached(groupID, userID)
		// 回源确认为成员后，本地索引做自愈补齐，减少后续重复回源。
		if v, ok := groupMemberLocalCache.Load(groupID); ok {
			e := v.(groupMemberLocalCacheEntry)
			if e.bitmap == nil {
				e.bitmap = roaring64.New()
			} else {
				e.bitmap = e.bitmap.Clone()
			}
			e.bitmap.Add(userID)
			groupMemberLocalCache.Store(groupID, e)
		} else {
			bm := roaring64.New()
			bm.Add(userID)
			storeGroupMembersCached(groupID, bm, time.Now().UnixMilli(), false)
		}
	}
	if memberResp.GetIsMember() {
		observe("fallback_grpc", "hit", fallbackStart)
		observe("total", "fallback_hit", start)
	} else {
		storeGroupMemberNegativeCached(groupID, userID)
		observe("fallback_grpc", "miss", fallbackStart)
		observe("total", "fallback_miss", start)
	}
	return memberResp.GetIsMember(), nil
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
	ids, err := s.listGroupMemberIDsWithErr(c, groupID)
	if err != nil {
		log.Printf("[trace=%s] list group members for push failed: gid=%d err=%v", c.GetString("trace_id"), groupID, err)
		return nil
	}
	return ids
}

func (s *HTTPServer) listGroupMemberIDsWithErr(c *gin.Context, groupID uint64) ([]uint64, error) {
	resp, err := s.groupClient.ListMembers(ctxWithTrace(c), &pbgroup.ListMembersRequest{GroupId: groupID})
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(resp.GetMembers()))
	for _, m := range resp.GetMembers() {
		ids = append(ids, m.GetUserId())
	}
	return ids, nil
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
	s.refreshGroupMemberSyncSnapshot(c, g.GetId())
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
	s.refreshGroupMemberSyncSnapshot(c, groupID)
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
	s.emitGroupMemberSyncDelete(c, groupID)
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
	s.refreshGroupMemberSyncSnapshot(c, groupID)
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
	s.refreshGroupMemberSyncSnapshot(c, groupID)
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
	start := time.Now()
	var validateDur, memberCheckDur, encodeDur, dispatchDur time.Duration
	observeFinal := func(result string) {
		observemetrics.ObserveGroupIngressStage("validate", result, validateDur.Seconds())
		observemetrics.ObserveGroupIngressStage("member_check", result, memberCheckDur.Seconds())
		observemetrics.ObserveGroupIngressStage("encode", result, encodeDur.Seconds())
		observemetrics.ObserveGroupIngressStage("dispatch", result, dispatchDur.Seconds())
		total := time.Since(start)
		observemetrics.ObserveGroupIngressStage("total", result, total.Seconds())
		if total > 500*time.Millisecond {
			log.Printf("[trace=%s] group ingress breakdown result=%s total_ms=%.2f validate_ms=%.2f member_check_ms=%.2f encode_ms=%.2f dispatch_ms=%.2f gid=%s",
				c.GetString("trace_id"), result,
				float64(total.Microseconds())/1000.0,
				float64(validateDur.Microseconds())/1000.0,
				float64(memberCheckDur.Microseconds())/1000.0,
				float64(encodeDur.Microseconds())/1000.0,
				float64(dispatchDur.Microseconds())/1000.0,
				c.Param("id"))
		}
	}

	validateStart := time.Now()
	userIDVal, _ := c.Get("userID")
	fromUserID := userIDVal.(uint)
	groupID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || groupID64 == 0 {
		validateDur = time.Since(validateStart)
		observeFinal("bad_request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group id"})
		return
	}
	groupID := uint(groupID64)
	var req gatewaymodel.SendGroupMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
		validateDur = time.Since(validateStart)
		observeFinal("bad_request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		validateDur = time.Since(validateStart)
		observeFinal("bad_request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "message content is empty"})
		return
	}
	if len(req.Content) > 4000 {
		validateDur = time.Since(validateStart)
		observeFinal("bad_request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "message content too long"})
		return
	}
	if req.ClientMsgID == "" {
		req.ClientMsgID = uuid.NewString()
	}
	validateDur = time.Since(validateStart)
	if s.redisClient != nil && config.GatewayGroupSendRateLimitEnabled {
		sec := time.Now().Unix()
		key := fmt.Sprintf("rate:group:send:%d:%d:%d", fromUserID, groupID, sec)
		n, err := s.redisClient.Incr(c.Request.Context(), key).Result()
		if err == nil {
			if n == 1 {
				_ = s.redisClient.Expire(c.Request.Context(), key, 2*time.Second).Err()
			}
			if n > 20 {
				log.Printf("[trace=%s] send group message rate limited: uid=%d gid=%d", c.GetString("trace_id"), fromUserID, groupID)
				observeFinal("rate_limited")
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "group message rate limited"})
				return
			}
		}
	}
	// 入 Kafka 前先做成员校验，避免非法消息进入异步链路。
	memberStart := time.Now()
	memberOK, err := s.isGroupMemberFast(c, uint64(groupID), uint64(fromUserID))
	memberCheckDur = time.Since(memberStart)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			observeFinal("bad_request")
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		log.Printf("[trace=%s] send group message is_member check failed: uid=%d gid=%d err=%v", c.GetString("trace_id"), fromUserID, groupID, err)
		observeFinal("member_check_error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !memberOK {
		log.Printf("[trace=%s] send group message denied: uid=%d gid=%d not member", c.GetString("trace_id"), fromUserID, groupID)
		observeFinal("forbidden")
		c.JSON(http.StatusForbidden, gin.H{"error": "user is not group member"})
		return
	}
	evt := groupmodel.GroupKafkaMessage{
		TraceID: c.GetString("trace_id"),
		EventID: req.ClientMsgID,
		GroupID: groupID,
		From:    fromUserID,
		Content: req.Content,
	}
	// 群消息统一走 group-message topic，由 group consumer 负责落库+扇出。
	// 先用 protobuf 编码，消费者端保留 JSON 兜底解码以兼容旧数据。
	encodeStart := time.Now()
	data, err := groupmodel.EncodeGroupKafkaMessagePB(evt)
	encodeDur = time.Since(encodeStart)
	if err != nil {
		observeFinal("encode_error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal group message failed"})
		return
	}
	// 用 group_id 作为 key：同群消息落同分区保序，不同群可跨分区并行消费。
	// 通过网关内批发送调度器聚合入 Kafka，降低高并发下单条同步发送抖动。
	if s.groupKafkaDispatch == nil {
		observeFinal("dispatch_error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "group kafka dispatcher not initialized"})
		return
	}
	dispatchStart := time.Now()
	if err := s.groupKafkaDispatch.submit(c.Request.Context(), strconv.FormatUint(groupID64, 10), data); err != nil {
		dispatchDur = time.Since(dispatchStart)
		log.Printf("[trace=%s] send group message kafka failed: uid=%d gid=%d event=%s err=%v", c.GetString("trace_id"), fromUserID, groupID, req.ClientMsgID, err)
		observeFinal("dispatch_error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	dispatchDur = time.Since(dispatchStart)
	if config.GatewayGroupSendBizLogEnabled {
		s.emitBizLog(c, "group message queued", req.ClientMsgID, map[string]interface{}{"group_id": uint64(groupID)})
	}
	observeFinal("ok")
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

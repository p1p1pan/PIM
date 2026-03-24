package mq

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	groupservice "pim/internal/group/service"
)

type routeCacheEntry struct {
	val       string
	expiresAt int64
}

// groupPushRouteCache 缓存 user -> gateway 路由，减少群扇出时 Redis 查询压力。
var groupPushRouteCache sync.Map // key: userID(uint64) -> routeCacheEntry

type memberCacheEntry struct {
	userIDs   []uint
	expiresAt int64
}

// groupMemberCache 缓存 group -> memberIDs，用于批消费场景降低重复查库。
var groupMemberCache sync.Map // key: groupID(uint) -> memberCacheEntry

// getMemberIDsWithCache 优先读取本地成员缓存，未命中再回源 group service。
func getMemberIDsWithCache(svc *groupservice.Service, groupID uint) ([]uint, error) {
	now := time.Now().UnixMilli()
	if v, ok := groupMemberCache.Load(groupID); ok {
		e := v.(memberCacheEntry)
		if e.expiresAt > now {
			return e.userIDs, nil
		}
		groupMemberCache.Delete(groupID)
	}
	memberIDs, err := svc.ListMemberUserIDsTrusted(groupID)
	if err != nil {
		return nil, err
	}
	ttl := int64(config.KafkaGroupMemberCacheTTLms)
	if ttl > 0 {
		groupMemberCache.Store(groupID, memberCacheEntry{
			userIDs:   memberIDs,
			expiresAt: now + ttl,
		})
	}
	return memberIDs, nil
}

// getRouteWithCache 查询用户连接路由：先本地缓存，再回源 Redis。
func getRouteWithCache(ctx context.Context, rdb *redis.Client, uid uint64) (string, error) {
	now := time.Now().UnixMilli()
	if v, ok := groupPushRouteCache.Load(uid); ok {
		e := v.(routeCacheEntry)
		if e.expiresAt > now {
			return e.val, nil
		}
		groupPushRouteCache.Delete(uid)
	}
	val, err := rdb.Get(ctx, fmt.Sprintf("ws:conn:%d", uid)).Result()
	if err != nil {
		if err == redis.Nil {
			return "", nil
		}
		return "", err
	}
	ttl := int64(config.KafkaConversationPushRouteCacheTTLms)
	if ttl > 0 && val != "" {
		groupPushRouteCache.Store(uid, routeCacheEntry{
			val:       val,
			expiresAt: now + ttl,
		})
	}
	return val, nil
}

// loadGroupRoutes 批量加载成员在线路由，命中缓存后仅对 miss 做 Redis Pipeline 查询。
func loadGroupRoutes(ctx context.Context, rdb *redis.Client, memberIDs []uint) (map[uint64]string, error) {
	routeByUID := make(map[uint64]string, len(memberIDs))
	missUIDs := make([]uint64, 0, len(memberIDs))
	for _, uid := range memberIDs {
		v, err := getRouteWithCache(ctx, rdb, uint64(uid))
		if err != nil {
			return nil, err
		}
		if v == "" {
			missUIDs = append(missUIDs, uint64(uid))
			continue
		}
		routeByUID[uint64(uid)] = v
	}
	if len(missUIDs) == 0 {
		return routeByUID, nil
	}
	pipe := rdb.Pipeline()
	cmds := make([]*redis.StringCmd, 0, len(missUIDs))
	for _, uid := range missUIDs {
		cmds = append(cmds, pipe.Get(ctx, fmt.Sprintf("ws:conn:%d", uid)))
	}
	_, _ = pipe.Exec(ctx)
	now := time.Now().UnixMilli()
	ttl := int64(config.KafkaConversationPushRouteCacheTTLms)
	for i, cmd := range cmds {
		val, err := cmd.Result()
		if err != nil || val == "" {
			continue
		}
		uid := missUIDs[i]
		routeByUID[uid] = val
		if ttl > 0 {
			groupPushRouteCache.Store(uid, routeCacheEntry{
				val:       val,
				expiresAt: now + ttl,
			})
		}
	}
	return routeByUID, nil
}

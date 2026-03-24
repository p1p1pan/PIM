package mq

import (
	"sync"
	"time"

	"pim/internal/config"
)

// routeCacheEntry 保存接收者到 gateway 路由的短期缓存条目。
type routeCacheEntry struct {
	route string
	expMs int64
}

// pushRouteCache 缓存 toUserID -> ws:conn 路由，降低热用户重复查 Redis 的开销。
var pushRouteCache sync.Map // key: uint(toUserID) -> routeCacheEntry

// routeCacheTTL 返回推送路由缓存 TTL；<=0 表示关闭缓存。
func routeCacheTTL() time.Duration {
	ms := config.KafkaConversationPushRouteCacheTTLms
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// getRouteFromCache 从本地缓存读取路由，并在过期时清理。
func getRouteFromCache(toUserID uint, nowMs int64) (string, bool) {
	v, ok := pushRouteCache.Load(toUserID)
	if !ok {
		return "", false
	}
	e, ok := v.(routeCacheEntry)
	if !ok || e.expMs <= nowMs || e.route == "" {
		pushRouteCache.Delete(toUserID)
		return "", false
	}
	return e.route, true
}

// putRouteCache 写入接收者路由缓存。
func putRouteCache(toUserID uint, route string, ttl time.Duration) {
	if ttl <= 0 || route == "" {
		return
	}
	exp := time.Now().Add(ttl).UnixMilli()
	pushRouteCache.Store(toUserID, routeCacheEntry{route: route, expMs: exp})
}

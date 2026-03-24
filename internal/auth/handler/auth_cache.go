package handler

import (
	"sync"
	"time"

	"pim/internal/config"
)

// authCacheEntry 保存网关鉴权缓存的值与过期时间。
type authCacheEntry struct {
	userID    uint
	expiresAt int64
}

// gatewayAuthCache 按 token 缓存 userID，减少高并发下重复 gRPC 鉴权。
var gatewayAuthCache sync.Map // key: token string -> authCacheEntry

// loadGatewayAuthCache 从本地缓存读取 token 对应 userID，并处理过期淘汰。
func loadGatewayAuthCache(token string) (uint, bool) {
	if token == "" || !config.GatewayAuthCacheEnabled {
		return 0, false
	}
	v, ok := gatewayAuthCache.Load(token)
	if !ok {
		return 0, false
	}
	e := v.(authCacheEntry)
	if e.expiresAt <= time.Now().UnixMilli() {
		gatewayAuthCache.Delete(token)
		return 0, false
	}
	return e.userID, true
}

// storeGatewayAuthCache 将 token->userID 写入本地缓存，TTL 由配置控制。
func storeGatewayAuthCache(token string, userID uint) {
	if token == "" || userID == 0 || !config.GatewayAuthCacheEnabled {
		return
	}
	ttl := int64(config.GatewayAuthCacheTTLms)
	if ttl <= 0 {
		ttl = 5000
	}
	gatewayAuthCache.Store(token, authCacheEntry{
		userID:    userID,
		expiresAt: time.Now().UnixMilli() + ttl,
	})
}

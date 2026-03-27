package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	authservice "pim/internal/auth/service"
	observemetrics "pim/internal/kit/observability/metrics"
)

// AuthMiddleware 本地 JWT 鉴权中间件。
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		observe := func(phase, result string) {
			observemetrics.ObserveGatewayAuthMiddleware("local", phase, result, c.FullPath(), time.Since(start).Seconds())
		}
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// 兼容 ws 握手等场景：允许通过 query token 传入。
			if tokenFromQuery := c.Query("token"); tokenFromQuery != "" {
				authHeader = "Bearer " + tokenFromQuery
			}
		}
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			observe("total", "missing_token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid token"})
			return
		}
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		userID, _, err := authservice.ParseToken(tokenString)
		if err != nil {
			observe("parse_token", "invalid_token")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		observe("parse_token", "ok")
		c.Set("userID", userID)
		// 后续 handler 统一从 context 读取 userID，不再重复解析 token。
		observe("total", "ok")
		c.Next()
	}
}

// GRPCMiddleware 调用 Auth gRPC 的 ValidateToken 做鉴权。
func GRPCMiddleware(client pbauth.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		observe := func(phase, result string, began time.Time) {
			observemetrics.ObserveGatewayAuthMiddleware("grpc", phase, result, c.FullPath(), time.Since(began).Seconds())
		}
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// 与本地中间件保持一致，支持 query token。
			if tokenFromQuery := c.Query("token"); tokenFromQuery != "" {
				authHeader = "Bearer " + tokenFromQuery
			}
		}
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			observe("total", "missing_token", start)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid token"})
			return
		}
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		cacheStart := time.Now()
		if uid, ok := loadGatewayAuthCache(tokenString); ok {
			observe("validate_token_cache", "hit", cacheStart)
			c.Set("userID", uid)
			observe("total", "ok_cache", start)
			c.Next()
			return
		}
		observe("validate_token_cache", "miss", cacheStart)
		validateStart := time.Now()
		resp, err := client.ValidateToken(c.Request.Context(), &pbauth.ValidateTokenRequest{Token: tokenString})
		if err != nil {
			if status.Code(err) == codes.Unauthenticated {
				observe("validate_token", "unauthenticated", validateStart)
				observe("total", "unauthenticated", start)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
				return
			}
			observe("validate_token", "error", validateStart)
			observe("total", "error", start)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "auth service error"})
			return
		}
		observe("validate_token", "ok", validateStart)
		uid := uint(resp.UserId)
		storeGatewayAuthCache(tokenString, uid)
		c.Set("userID", uid)
		observe("total", "ok", start)
		c.Next()
	}
}

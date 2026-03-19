package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	authservice "pim/internal/auth/service"
)

// AuthMiddleware 本地 JWT 鉴权中间件。
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			if tokenFromQuery := c.Query("token"); tokenFromQuery != "" {
				authHeader = "Bearer " + tokenFromQuery
			}
		}
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid token"})
			return
		}
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		userID, _, err := authservice.ParseToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("userID", userID)
		c.Next()
	}
}

// GRPCMiddleware 调用 Auth gRPC 的 ValidateToken 做鉴权。
func GRPCMiddleware(client pbauth.AuthServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			if tokenFromQuery := c.Query("token"); tokenFromQuery != "" {
				authHeader = "Bearer " + tokenFromQuery
			}
		}
		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid token"})
			return
		}
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		resp, err := client.ValidateToken(c.Request.Context(), &pbauth.ValidateTokenRequest{Token: tokenString})
		if err != nil {
			if status.Code(err) == codes.Unauthenticated {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
				return
			}
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "auth service error"})
			return
		}
		c.Set("userID", uint(resp.UserId))
		c.Next()
	}
}

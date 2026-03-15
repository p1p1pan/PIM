// Package auth 提供 JWT 签发、解析与 Gin 鉴权中间件，供 Gateway、各业务服务复用。
package auth

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
)

// Claims 为 JWT payload 中的业务字段，sub 存用户 ID，username 存用户名；RegisteredClaims 含过期时间等。
type Claims struct {
	UserID   uint   `json:"sub"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// GenerateToken 使用 config.JWTSecret 签发 JWT，过期时间由 ttl 指定，供登录成功时返回给客户端。
func GenerateToken(userID uint, username string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.JWTSecret))
}

// ParseToken 用 config.JWTSecret 校验签名并解析 JWT，成功时返回 userID 与 claims，失败返回 error。
func ParseToken(tokenString string) (uint, *Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(config.JWTSecret), nil
	})
	if err != nil {
		return 0, nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return 0, nil, fmt.Errorf("invalid token claims")
	}
	return claims.UserID, claims, nil
}

// AuthMiddleware 返回 Gin 鉴权中间件：从请求头 Authorization: Bearer <token> 或 query token 解析 JWT，
// 校验通过后将 userID 写入 c.Set("userID", userID)，后续 handler 通过 c.Get("userID") 获取当前用户 ID。
// 若缺少或无效 token 则直接 401 并 Abort，不再执行后续 handler。
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
		userID, _, err := ParseToken(tokenString)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set("userID", userID)
		c.Next()
	}
}

// GRPCMiddleware 供 Gateway 使用：从请求拿 token，调 Auth 服务的 gRPC ValidateToken 校验，成功则 c.Set("userID", ...)。
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
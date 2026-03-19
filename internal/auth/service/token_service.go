package service

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"pim/internal/auth/model"
	"pim/internal/config"
)

// GenerateToken 使用配置中的 JWTSecret 签发 token。
func GenerateToken(userID uint, username string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := model.Claims{
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

// ParseToken 校验签名并解析 token，并返回用户声明。
func ParseToken(tokenString string) (uint, *model.Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &model.Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(config.JWTSecret), nil
	})
	if err != nil {
		return 0, nil, err
	}
	claims, ok := token.Claims.(*model.Claims)
	if !ok || !token.Valid {
		return 0, nil, fmt.Errorf("invalid token claims")
	}
	return claims.UserID, claims, nil
}

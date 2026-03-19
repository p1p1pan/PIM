package model

import "github.com/golang-jwt/jwt/v5"

// Claims 为 JWT payload 中的业务字段。
type Claims struct {
	UserID   uint   `json:"sub"`
	Username string `json:"username"`
	jwt.RegisteredClaims
}

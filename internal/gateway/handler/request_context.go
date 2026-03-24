package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/metadata"
)

// ctxWithTrace 将 Gin 上下文中的 trace_id 透传到 gRPC metadata。
func ctxWithTrace(c *gin.Context) context.Context {
	traceID, _ := c.Get("trace_id")
	idStr, _ := traceID.(string)
	if idStr == "" {
		return c.Request.Context()
	}
	md := metadata.Pairs("x-trace-id", idStr)
	return metadata.NewOutgoingContext(c.Request.Context(), md)
}

// requireUserID 从鉴权中间件注入的上下文读取 userID；缺失时直接中断请求。
func requireUserID(c *gin.Context) (uint, bool) {
	userIDVal, ok := c.Get("userID")
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "user not authenticated"})
		return 0, false
	}
	userID, ok := userIDVal.(uint)
	if !ok || userID == 0 {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "invalid user id"})
		return 0, false
	}
	return userID, true
}

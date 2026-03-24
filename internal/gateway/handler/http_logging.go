package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"pim/internal/config"
	logkit "pim/internal/log/kit"
	logmodel "pim/internal/log/model"
)

// AccessLogMiddleware 将网关请求访问日志异步写入 log-topic。
func (s *HTTPServer) AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 与 Gin 控制台日志一致：探活与指标抓取不写 log-topic，避免刷屏与 ES 噪音。
		if p := c.Request.URL.Path; p == "/metrics" || p == "/health" {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()
		if s == nil || s.kafkaProducer == nil {
			return
		}
		traceID := c.GetString("trace_id")
		entry := logmodel.Log{
			TS:        time.Now(),
			Level:     "info",
			Service:   "gateway",
			TraceID:   traceID,
			Msg:       "http access",
			Path:      c.FullPath(),
			LatencyMS: time.Since(start).Milliseconds(),
			ErrorCode: fmt.Sprintf("%d", c.Writer.Status()),
		}
		if entry.Path == "" {
			entry.Path = c.Request.URL.Path
		}
		if uid, ok := c.Get("userID"); ok {
			if id, ok := uid.(uint); ok {
				entry.UserID = uint64(id)
			}
		}
		entry, ok := logkit.ApplyPolicy(entry, config.LogInfoSamplePct)
		if !ok {
			return
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return
		}
		s.sendLogAsync("log-topic", "", data)
	}
}

// sendLogAsync 异步发送日志到 Kafka，避免阻塞业务请求。
func (s *HTTPServer) sendLogAsync(topic, key string, data []byte) {
	if s == nil || s.kafkaProducer == nil || len(data) == 0 {
		return
	}
	payload := append([]byte(nil), data...)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_ = s.kafkaProducer.SendMessage(ctx, topic, key, payload)
	}()
}

// emitBizLog 将关键业务事件写入 log-topic，供 log-service 聚合检索。
func (s *HTTPServer) emitBizLog(c *gin.Context, msg string, eventID string, extra map[string]interface{}) {
	if s == nil || s.kafkaProducer == nil {
		return
	}
	entry := logmodel.Log{
		TS:      time.Now(),
		Level:   "info",
		Service: "gateway",
		TraceID: c.GetString("trace_id"),
		Msg:     msg,
		Path:    c.FullPath(),
		EventID: eventID,
	}
	if entry.Path == "" {
		entry.Path = c.Request.URL.Path
	}
	if uid, ok := c.Get("userID"); ok {
		if id, ok := uid.(uint); ok {
			entry.UserID = uint64(id)
		}
	}
	if extra != nil {
		if gid, ok := extra["group_id"].(uint64); ok {
			entry.GroupID = gid
		}
		if cid, ok := extra["conversation_id"].(string); ok {
			entry.ConversationID = cid
		}
	}
	entry, ok := logkit.ApplyPolicy(entry, config.LogInfoSamplePct)
	if !ok {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	s.sendLogAsync("log-topic", "", data)
}

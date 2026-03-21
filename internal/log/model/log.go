package model

import "time"

// Log 是统一日志结构，字段与文档中的阶段4规范对齐。
type Log struct {
	TS             time.Time `json:"ts"`
	Level          string    `json:"level"`
	Service        string    `json:"service"`
	TraceID        string    `json:"trace_id"`
	Msg            string    `json:"msg"`
	EventID        string    `json:"event_id,omitempty"`
	UserID         uint64    `json:"user_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	GroupID        uint64    `json:"group_id,omitempty"`
	Path           string    `json:"path,omitempty"`
	LatencyMS      int64     `json:"latency_ms,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
}

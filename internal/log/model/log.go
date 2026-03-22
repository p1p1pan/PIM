package model

import "time"

// Log 是统一日志结构，字段与文档中的阶段4规范对齐。
// 查询语义：按 trace_id 或 event_id 查询时需保证非空；生产者应在无上游 trace 时使用 "unknown"。
type Log struct {
	TS             time.Time `json:"ts"`
	Level          string    `json:"level"`
	Service        string    `json:"service"`
	TraceID        string    `json:"trace_id"`
	Msg            string    `json:"msg"`
	EventID        string    `json:"event_id,omitempty"` // 建议：消息类用 ClientMsgID/EventID，file-scan 用 file-scan:fileID
	UserID         uint64    `json:"user_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	GroupID        uint64    `json:"group_id,omitempty"`
	Path           string    `json:"path,omitempty"`
	LatencyMS      int64     `json:"latency_ms,omitempty"`
	ErrorCode      string    `json:"error_code,omitempty"`
}

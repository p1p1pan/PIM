package model

import (
	"encoding/json"
	"strings"
)

// EmptyMentionMetaJSON 为 V1 空对象，与 doc/group-mention-v1-implementation.md 一致。
const EmptyMentionMetaJSON = `{"v":1,"at_all":false,"user_ids":[],"spans":[]}`

// NormalizeMentionMeta 消费侧：非法或空则回落为 EmptyMentionMetaJSON。
func NormalizeMentionMeta(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return EmptyMentionMetaJSON
	}
	if !json.Valid([]byte(s)) {
		return EmptyMentionMetaJSON
	}
	var m struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil || m.V != 1 {
		return EmptyMentionMetaJSON
	}
	return s
}

// 群聊 mention_meta 构建，见 doc/group-mention-v1-implementation.md。供 handleSendGroupMessage 在写 Kafka 前调用。
package handler

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	groupmodel "pim/internal/group/model"
)

func isASCIILetterMention(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isLoginRuneMention(r rune) bool {
	if isASCIILetterMention(r) {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	return r == '_'
}

func matchMentionAllZh(r []rune, i int) bool {
	if i+3 >= len(r) {
		return false
	}
	return r[i] == '@' && r[i+1] == '所' && r[i+2] == '有' && r[i+3] == '人'
}

func matchMentionAllEN(r []rune, i int) bool {
	if i+3 >= len(r) {
		return false
	}
	if r[i] != '@' {
		return false
	}
	a, b, c := r[i+1], r[i+2], r[i+3]
	if !isASCIILetterMention(a) || !isASCIILetterMention(b) || !isASCIILetterMention(c) {
		return false
	}
	low := strings.ToLower(string([]rune{a, b, c}))
	if low != "all" {
		return false
	}
	if i+4 < len(r) && isLoginRuneMention(r[i+4]) {
		return false
	}
	return true
}

// buildGroupMentionMeta 按文档扫描 content，产出 mention_meta JSON；不修改 content。
// getUID 返回 0 表示无此用户；isMem 为群内校验。
func buildGroupMentionMeta(ctx context.Context, content string, groupID uint,
	getUID func(context.Context, string) (uint64, error),
	isMem func(context.Context, uint, uint64) (bool, error),
) (string, error) {
	r := []rune(content)
	n := len(r)
	var spans []map[string]any
	var uids []uint
	i := 0
	for i < n {
		if i+1 < n && r[i] == '@' && r[i+1] == '@' {
			i += 2
			continue
		}
		if r[i] != '@' {
			i++
			continue
		}
		if matchMentionAllZh(r, i) {
			spans = append(spans, map[string]any{"b": i, "e": i + 4, "k": "all"})
			i += 4
			continue
		}
		if matchMentionAllEN(r, i) {
			spans = append(spans, map[string]any{"b": i, "e": i + 4, "k": "all"})
			i += 4
			continue
		}
		j := i + 1
		for j < n && isLoginRuneMention(r[j]) {
			j++
		}
		if j == i+1 {
			i++
			continue
		}
		login := string(r[i+1 : j])
		uid, err := getUID(ctx, login)
		if err != nil {
			return "", err
		}
		if uid == 0 {
			i++
			continue
		}
		ok, err := isMem(ctx, groupID, uid)
		if err != nil {
			return "", err
		}
		if !ok {
			i++
			continue
		}
		uu := uint(uid)
		spans = append(spans, map[string]any{"b": i, "e": j, "k": "user", "u": uu})
		uids = append(uids, uu)
		i = j
		continue
	}

	atAll := false
	for _, sp := range spans {
		if sp["k"] == "all" {
			atAll = true
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	uniq := uids[:0]
	for _, x := range uids {
		if len(uniq) == 0 || uniq[len(uniq)-1] != x {
			uniq = append(uniq, x)
		}
	}
	uids = uniq

	m := struct {
		V       int         `json:"v"`
		AtAll   bool        `json:"at_all"`
		UserIDs []uint      `json:"user_ids"`
		Spans   interface{} `json:"spans"`
	}{V: 1, AtAll: atAll, UserIDs: uids, Spans: groupMentionSpanArray(spans)}

	b, err := json.Marshal(m)
	if err != nil {
		return groupmodel.EmptyMentionMetaJSON, err
	}
	return string(b), nil
}

func groupMentionSpanArray(spans []map[string]any) []any {
	if len(spans) == 0 {
		return []any{}
	}
	out := make([]any, 0, len(spans))
	for _, sp := range spans {
		if sp["k"] == "all" {
			out = append(out, map[string]any{
				"b": sp["b"],
				"e": sp["e"],
				"k": "all",
			})
		} else {
			out = append(out, map[string]any{
				"b": sp["b"],
				"e": sp["e"],
				"k": "user",
				"u": sp["u"],
			})
		}
	}
	return out
}

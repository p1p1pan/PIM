package kit

import (
	"math/rand"
	"strings"
	"time"

	logmodel "pim/internal/log/model"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// ApplyPolicy 对日志执行脱敏和采样策略。
// - error/warn 全量保留；
// - info 按 samplePct 采样；
// - msg/path 做基础脱敏（token、URL query）。
func ApplyPolicy(entry logmodel.Log, samplePct int) (logmodel.Log, bool) {
	entry.Msg = sanitizeText(entry.Msg)
	entry.Path = sanitizePath(entry.Path)

	level := strings.ToLower(strings.TrimSpace(entry.Level))
	if level == "" {
		level = "info"
		entry.Level = "info"
	}
	if level == "error" || level == "warn" {
		return entry, true
	}
	if samplePct <= 0 {
		return entry, false
	}
	if samplePct >= 100 {
		return entry, true
	}
	return entry, rand.Intn(100) < samplePct
}

func sanitizeText(s string) string {
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	if idx := strings.Index(lower, "bearer "); idx >= 0 {
		return s[:idx] + "Bearer ***"
	}
	// 脱敏预签名 URL 查询串，避免将签名参数打入日志。
	if q := strings.Index(s, "?"); q >= 0 {
		return s[:q] + "?***"
	}
	return s
}

func sanitizePath(path string) string {
	if path == "" {
		return path
	}
	if q := strings.Index(path, "?"); q >= 0 {
		return path[:q] + "?***"
	}
	return path
}

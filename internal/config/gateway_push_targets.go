package config

import "strings"

// ParseGatewayPushTargets parses "gateway-1=localhost:8090,gateway-2=localhost:8091".
// If raw is empty or invalid, falls back to gateway-1 -> fallbackTarget.
func ParseGatewayPushTargets(raw, fallbackTarget string) map[string]string {
	out := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw != "" {
		parts := strings.Split(raw, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			kv := strings.SplitN(p, "=", 2)
			if len(kv) != 2 {
				continue
			}
			node := strings.TrimSpace(kv[0])
			addr := strings.TrimSpace(kv[1])
			if node == "" || addr == "" {
				continue
			}
			out[node] = addr
		}
	}
	if len(out) == 0 && strings.TrimSpace(fallbackTarget) != "" {
		out["gateway-1"] = strings.TrimSpace(fallbackTarget)
	}
	return out
}

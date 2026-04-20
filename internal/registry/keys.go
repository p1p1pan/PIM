package registry

import "strings"

// EndpointsPrefix 返回某逻辑服务在 etcd 下的前缀（含末尾 /，便于 WithPrefix Watch）。
func EndpointsPrefix(basePrefix, logical string) string {
	base := strings.TrimRight(strings.TrimSpace(basePrefix), "/")
	return base + "/endpoints/" + strings.TrimSpace(logical) + "/"
}

func endpointInstanceKey(basePrefix, logical, instanceID string) string {
	return EndpointsPrefix(basePrefix, logical) + strings.TrimSpace(instanceID)
}

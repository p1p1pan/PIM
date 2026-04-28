package observe

// DefaultAPIRouteCatalog 与 gateway 注册的主要 /api 路径一致，用于无 gateway 旁路时补全概览表。
func DefaultAPIRouteCatalog() []string {
	return []string{
		"/api/v1/me",
		"/api/v1/friends",
		"/api/v1/friends/requests",
		"/api/v1/friends/requests/incoming",
		"/api/v1/friends/requests/outgoing",
		"/api/v1/conversations",
		"/api/v1/messages",
		"/api/v1/groups",
		"/api/v1/groups/conversations",
		"/api/v1/files/prepare",
		"/api/v1/login",
		"/api/v1/register",
		"/api/v1/admin/health",
		"/api/v1/admin/metrics",
		"/api/v1/admin/observability/overview",
		"/api/v1/admin/observability/bench-run",
		"/api/v1/logs/trace/:trace_id",
		"/api/v1/logs/search",
		"/api/v1/logs/filter",
	}
}

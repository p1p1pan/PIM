package observe

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册只读 admin + 日志相关路由（与迁出前 gateway 的 public 段行为一致，均无需登录）。
func RegisterRoutes(s *Service, r *gin.Engine) {
	public := r.Group("/api/v1")
	public.GET("/admin/health", s.handleAdminHealth)
	public.GET("/admin/metrics", s.handleAdminMetrics)
	public.GET("/admin/observability/overview", s.handleAdminObservabilityOverview)
	public.POST("/admin/observability/bench-run", s.handleAdminBenchRun)
	public.GET("/logs/trace/:trace_id", s.handleLogsByTrace)
	public.GET("/logs/search", s.handleLogsByEvent)
	public.GET("/logs/filter", s.handleLogsFilter)
}

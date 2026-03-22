package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ginProbeSkipPaths 为 Prometheus 抓取与 Docker/K8s 探活路径；不打 Gin 控制台访问日志，避免刷屏。
var ginProbeSkipPaths = []string{"/metrics", "/metrics/", "/health"}

// UseGinDefaultMiddleware 等价于 gin.Default() 的 Logger+Recovery，但跳过探针路径。
// 各业务服务与网关应使用 gin.New() + 本函数 + RegisterMetricsRoute，替代 gin.Default()。
func UseGinDefaultMiddleware(r *gin.Engine) {
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		SkipPaths: ginProbeSkipPaths,
	}))
	r.Use(gin.Recovery())
}

// RegisterMetricsRoute registers a shared /metrics endpoint for Prometheus scraping.
func RegisterMetricsRoute(r *gin.Engine) {
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
	r.GET("/metrics/", func(c *gin.Context) {
		// Keep a trailing-slash alias to reduce scraping misconfiguration risks.
		promhttp.Handler().ServeHTTP(c.Writer, c.Request)
	})
}

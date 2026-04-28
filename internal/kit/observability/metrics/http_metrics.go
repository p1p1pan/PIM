package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pim_http_requests_total",
			Help: "Total HTTP requests grouped by service/method/route/status.",
		},
		[]string{"service", "method", "route", "status"},
	)
	httpRequestDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pim_http_request_duration_seconds",
			Help:    "HTTP request latency in seconds grouped by service/method/route.",
			// 原最大有限桶 5s 时 histogram_quantile 的 p95 常「顶在 5000ms」；补 3/4/7.5/10/20s 等便于区分尾延迟（+Inf 由 client 补全）。
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.3, 0.5, 1, 2, 3, 4, 5, 7.5, 10, 20, 30},
		},
		[]string{"service", "method", "route"},
	)
)

// HTTPServerMetricsMiddleware records unified HTTP metrics for one service.
func HTTPServerMetricsMiddleware(service string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		method := c.Request.Method
		status := strconv.Itoa(c.Writer.Status())

		httpRequestsTotal.WithLabelValues(service, method, route, status).Inc()
		httpRequestDurationSeconds.WithLabelValues(service, method, route).Observe(time.Since(start).Seconds())
	}
}

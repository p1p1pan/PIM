package metrics

import "github.com/prometheus/client_golang/prometheus"
import "github.com/prometheus/client_golang/prometheus/promauto"

var gatewayAuthMiddlewareDurationSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "pim_gateway_auth_middleware_duration_seconds",
		Help:    "Gateway auth middleware latency in seconds by mode/phase/result/route.",
		Buckets: []float64{0.0002, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.3, 0.5, 1, 2},
	},
	[]string{"mode", "phase", "result", "route"},
)

// ObserveGatewayAuthMiddleware records auth middleware latency breakdown.
func ObserveGatewayAuthMiddleware(mode, phase, result, route string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	if mode == "" {
		mode = "unknown"
	}
	if phase == "" {
		phase = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	if route == "" {
		route = "unknown"
	}
	gatewayAuthMiddlewareDurationSeconds.WithLabelValues(mode, phase, result, route).Observe(seconds)
}

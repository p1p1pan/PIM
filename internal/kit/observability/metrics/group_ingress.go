package metrics

import "github.com/prometheus/client_golang/prometheus/promauto"
import "github.com/prometheus/client_golang/prometheus"

var groupIngressStageDurationSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "pim_group_ingress_stage_duration_seconds",
		Help:    "Gateway group message ingress stage latency in seconds by stage/result.",
		Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.3, 0.5, 1, 2, 5},
	},
	[]string{"stage", "result"},
)

var groupMemberCheckStageDurationSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "pim_group_member_check_stage_duration_seconds",
		Help:    "Gateway group member check stage latency in seconds by stage/result.",
		Buckets: []float64{0.0002, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.3, 0.5, 1, 2},
	},
	[]string{"stage", "result"},
)

// ObserveGroupIngressStage records per-stage latency in group ingress path.
func ObserveGroupIngressStage(stage, result string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	if stage == "" {
		stage = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	groupIngressStageDurationSeconds.WithLabelValues(stage, result).Observe(seconds)
}

// ObserveGroupMemberCheckStage records per-stage latency in member_check path.
func ObserveGroupMemberCheckStage(stage, result string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	if stage == "" {
		stage = "unknown"
	}
	if result == "" {
		result = "unknown"
	}
	groupMemberCheckStageDurationSeconds.WithLabelValues(stage, result).Observe(seconds)
}

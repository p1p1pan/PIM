package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	gatewayWSConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "pim_gateway_ws_connections",
			Help: "Current number of active WebSocket connections on this gateway node.",
		},
	)
	gatewayWSConnectionsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "pim_gateway_ws_connections_total",
			Help: "Total successful WebSocket connection upgrades.",
		},
	)
	gatewayWSDisconnectsTotal = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "pim_gateway_ws_disconnects_total",
			Help: "Total WebSocket disconnections after successful upgrades.",
		},
	)
	gatewayPushTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pim_gateway_push_total",
			Help: "Gateway internal push attempts grouped by result.",
		},
		[]string{"result"},
	)
)

func ObserveGatewayWSConnected() {
	gatewayWSConnections.Inc()
	gatewayWSConnectionsTotal.Inc()
}

func ObserveGatewayWSDisconnected() {
	gatewayWSConnections.Dec()
	gatewayWSDisconnectsTotal.Inc()
}

func ObserveGatewayPush(result string) {
	gatewayPushTotal.WithLabelValues(result).Inc()
}

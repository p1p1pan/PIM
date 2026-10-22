package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// gatewayWSSendDurationSeconds 记录网关 WS 上行处理时延：
//   - 从 handler 读到一帧 WS 消息起，到 writeJSONToUser({type:"ack"}) 返回为止。
//   - 对应 bench.ack_qps 的服务端视角；bench 客户端侧再加网络 RTT。
//
// op: im / group / read（现仅 im 在用；其余预留）。
// result: ok / error（Kafka 入队失败返回 ack_error 也视为 result=error 上报）。
var gatewayWSSendDurationSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "pim_gateway_ws_send_duration_seconds",
		Help: "Gateway WebSocket send (ingress -> ack write) duration seconds.",
		// 聚焦毫秒级：bench.ack 通常 <5ms；5 以上均归入 +Inf，作为异常尾噪提醒。
		Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
	},
	[]string{"op", "result"},
)

// imE2EServerSeconds 记录单聊消息的服务端 e2e 时延：
//   - 从 sender 网关收到 WS 帧起，到推送消费者调用 gateway PushBatchToConn 成功为止。
//   - 时钟源为 ingress_ts_ns (Kafka header)；跨进程依赖 NTP 同步。
//
// topic: im-message（当前仅单聊；群消息后续可接入）。
var imE2EServerSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "pim_im_e2e_server_seconds",
		Help: "Server-side end-to-end latency from sender gateway WS ingress to receiver push dispatch completion.",
		// 覆盖 ms 到 s 范围，和 bench.e2e 刻度保持一致。
		Buckets: []float64{0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
	},
	[]string{"topic"},
)

// ObserveGatewayWSSendDuration 记录网关 WS 发 ack 的耗时。
func ObserveGatewayWSSendDuration(op, result string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	gatewayWSSendDurationSeconds.WithLabelValues(op, result).Observe(seconds)
}

// ObserveIMe2eServerSeconds 记录单条单聊消息的服务端 e2e 时延。
// 调用方应先校验 ingress_ts_ns > 0 再调用（避免把"没带 header"的消息观测成非常大/负值）。
func ObserveIMe2eServerSeconds(topic string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	imE2EServerSeconds.WithLabelValues(topic).Observe(seconds)
}

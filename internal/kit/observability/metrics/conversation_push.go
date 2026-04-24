package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// conversationPushDropped 记录 im-message-push 消费者在发给任何 gateway 之前就被静默丢弃的消息条数，
// 用于区分"消息没送达用户"到底是卡在路由查询（route_missing）、路由格式（route_malformed），还是
// 找不到对应 gateway 节点（node_not_found）。
var conversationPushDropped = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "pim_conversation_push_dropped_total",
		Help: "Messages silently dropped by im-message-push consumer before reaching any gateway. Labeled by reason.",
	},
	[]string{"reason"},
)

func ObserveConversationPushDropped(reason string) {
	conversationPushDropped.WithLabelValues(reason).Inc()
}

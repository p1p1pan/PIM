package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var kafkaMessagesTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "pim_kafka_messages_total",
		Help: "Total Kafka messages grouped by topic/direction/result.",
	},
	[]string{"topic", "direction", "result"},
)

var kafkaHandlerDurationSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "pim_kafka_handler_duration_seconds",
		Help: "Kafka consumer handler duration in seconds grouped by topic/result.",
		// 覆盖常见消费处理耗时区间：毫秒级到秒级，便于估算 topic p95。
		Buckets: []float64{0.005, 0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5},
	},
	[]string{"topic", "result"},
)

// ObserveKafkaProduce records producer result for one topic.
func ObserveKafkaProduce(topic, result string) {
	kafkaMessagesTotal.WithLabelValues(topic, "produce", result).Inc()
}

// ObserveKafkaConsume records consumer handler result for one topic.
func ObserveKafkaConsume(topic, result string) {
	kafkaMessagesTotal.WithLabelValues(topic, "consume", result).Inc()
}

// ObserveKafkaHandlerDuration records Kafka consumer handler duration for one topic.
func ObserveKafkaHandlerDuration(topic, result string, seconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	kafkaHandlerDurationSeconds.WithLabelValues(topic, result).Observe(seconds)
}

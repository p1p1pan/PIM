package kafka

import (
	"strconv"

	"github.com/Shopify/sarama"
)

// HeaderIngressTsNs 是 Kafka 消息头里携带 sender 侧入口时间（Unix 纳秒）的键。
// 用于跨进程 e2e 延迟测量，不影响 proto schema 兼容性。
const HeaderIngressTsNs = "x-pim-ingress-ts-ns"

// EncodeIngressTsNs 把 ns 级时间戳编码为 ASCII 字节，避免 endian 差异引起的解析风险。
func EncodeIngressTsNs(ns int64) []byte {
	return []byte(strconv.FormatInt(ns, 10))
}

// DecodeIngressTsNs 从 ASCII 字节解析 ns 级时间戳；解析失败返回 0, false。
func DecodeIngressTsNs(v []byte) (int64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(string(v), 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ExtractIngressTsNsFromHeaders 扫描 Sarama header 列表取出 ingress_ts_ns；
// 返回 (0, false) 表示老消息未带 header（调用方应跳过 e2e 观测，不 panic）。
func ExtractIngressTsNsFromHeaders(headers []*sarama.RecordHeader) (int64, bool) {
	for _, h := range headers {
		if h == nil {
			continue
		}
		if string(h.Key) == HeaderIngressTsNs {
			return DecodeIngressTsNs(h.Value)
		}
	}
	return 0, false
}

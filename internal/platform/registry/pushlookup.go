package registry

import pbgateway "pim/internal/gateway/pb"

// GatewayPushClientLookup 供 Kafka 消费路径按 gateway 节点取 Push gRPC 客户端（线程安全快照）。
type GatewayPushClientLookup interface {
	Snapshot() map[string]pbgateway.PushServiceClient
}

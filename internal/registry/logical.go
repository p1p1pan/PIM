package registry

// Logical gRPC 服务名（etcd 路径段与 pim-etcd resolver 路径一致）。
const (
	LogicalUser         = "user"
	LogicalAuth         = "auth"
	LogicalFriend       = "friend"
	LogicalConversation = "conversation"
	LogicalGroup        = "group"
	LogicalFile         = "file"
	LogicalGatewayPush  = "gateway-push"
)

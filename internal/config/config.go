package config

import (
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// 配置信息：环境变量优先，未设置时回落默认值。
var (
	DBHost     = getenv("POSTGRES_HOST", "localhost")
	DBPort     = getenv("POSTGRES_PORT", "55432")
	DBUser     = getenv("POSTGRES_USER", "pim")
	DBPassword = getenv("POSTGRES_PASSWORD", "pim")
	DBName     = getenv("POSTGRES_DB", "pim")

	// PostgreSQL 连接池（按进程）。默认值按“本地压测”提升一档，降低登录高并发下排队。
	// 多服务同时连同一库时，各进程 MaxOpen 之和应 < PostgreSQL max_connections。
	DBMaxOpenConns       = getenvInt("POSTGRES_MAX_OPEN_CONNS", 80)
	DBMaxIdleConns       = getenvInt("POSTGRES_MAX_IDLE_CONNS", 40)
	DBConnMaxLifetimeSec = getenvInt("POSTGRES_CONN_MAX_LIFETIME_SEC", 300)  // 0 表示不限制生命周期
	DBConnMaxIdleTimeSec = getenvInt("POSTGRES_CONN_MAX_IDLE_TIME_SEC", 120) // 0 表示不限制空闲时间

	JWTSecret = getenv("JWT_SECRET", "pim-dev-secret") // JWT 签名密钥

	RedisAddr     = getenv("REDIS_ADDR", "localhost:6379")
	RedisPassword = getenv("REDIS_PASSWORD", "")
	RedisDB       = getenvInt("REDIS_DB", 0)
	// Gateway 批量推送并行度：按 ToUserID 分链并行，同一用户链路仍串行保序。
	GatewayPushBatchParallel = getenvInt("GATEWAY_PUSH_BATCH_PARALLEL", 32)
	// Gateway 群成员 ready 本地短缓存（毫秒），用于减少每条消息都打 Redis EXISTS。
	GatewayGroupReadyCacheTTLms = getenvInt("GATEWAY_GROUP_READY_CACHE_TTL_MS", 60000)
	// Gateway 群成员本地缓存（gid -> members set）TTL，降低 ack 热路径逐条 Redis 成员查询抖动。
	GatewayGroupMemberLocalCacheTTLms = getenvInt("GATEWAY_GROUP_MEMBER_LOCAL_CACHE_TTL_MS", 60000)
	// 群发入口限流（Redis INCR）开关；高压压测下可关闭以降低 ingress 抖动。
	GatewayGroupSendRateLimitEnabled = getenvBool("GATEWAY_GROUP_SEND_RATE_LIMIT_ENABLED", false)
	// member_check 负缓存 TTL（毫秒）：Redis miss + fallback miss 后短时缓存，避免热 miss 风暴反复回源 gRPC。
	GatewayGroupMemberNegativeCacheTTLms = getenvInt("GATEWAY_GROUP_MEMBER_NEGATIVE_CACHE_TTL_MS", 1500)
	// 群发成功路径业务日志（log-topic）开关；默认关闭避免每条群消息额外一次 Kafka 同步发送。
	GatewayGroupSendBizLogEnabled = getenvBool("GATEWAY_GROUP_SEND_BIZ_LOG_ENABLED", false)
	// Gateway 群消息入 Kafka 聚合发送参数：减少单条同步发送抖动。
	GatewayGroupKafkaBatchSize   = getenvInt("GATEWAY_GROUP_KAFKA_BATCH_SIZE", 128)
	GatewayGroupKafkaBatchWaitMs = getenvInt("GATEWAY_GROUP_KAFKA_BATCH_WAIT_MS", 1)
	GatewayGroupKafkaQueueSize   = getenvInt("GATEWAY_GROUP_KAFKA_QUEUE_SIZE", 16384)
	// submit 阶段超时（毫秒）：分别限制“入 worker 队列等待”和“等待 flush 完成回执”。
	GatewayGroupKafkaEnqueueTimeoutMs = getenvInt("GATEWAY_GROUP_KAFKA_ENQUEUE_TIMEOUT_MS", 80)
	GatewayGroupKafkaWaitTimeoutMs    = getenvInt("GATEWAY_GROUP_KAFKA_WAIT_TIMEOUT_MS", 300)
	// 群消息入口是否同步等待 Kafka 发送结果；false 时仅保证入队成功即可返回（更低延迟）。
	GatewayGroupKafkaWaitResult = getenvBool("GATEWAY_GROUP_KAFKA_WAIT_RESULT", false)
	// worker flush 调用 Kafka 发送超时（毫秒）。
	GatewayGroupKafkaFlushTimeoutMs = getenvInt("GATEWAY_GROUP_KAFKA_FLUSH_TIMEOUT_MS", 2000)
	// Gateway 群消息入 Kafka 分片并行度：同 group 按 key 固定分片保序，不同 group 并行发。
	GatewayGroupKafkaParallel = getenvInt("GATEWAY_GROUP_KAFKA_PARALLEL", 24)
	// Gateway 启动预热：从 Redis ready 索引加载活跃群成员到本地索引，降低冷启动 member_check 抖动。
	GatewayGroupMemberPrewarmEnabled    = getenvBool("GATEWAY_GROUP_MEMBER_PREWARM_ENABLED", true)
	GatewayGroupMemberPrewarmLimit      = getenvInt("GATEWAY_GROUP_MEMBER_PREWARM_LIMIT", 2000)
	GatewayGroupMemberPrewarmTimeoutSec = getenvInt("GATEWAY_GROUP_MEMBER_PREWARM_TIMEOUT_SEC", 20)
	// Gateway 鉴权中间件本地短缓存（token -> uid），降低高并发下每请求 ValidateToken gRPC 开销。
	GatewayAuthCacheEnabled = getenvBool("GATEWAY_AUTH_CACHE_ENABLED", true)
	GatewayAuthCacheTTLms   = getenvInt("GATEWAY_AUTH_CACHE_TTL_MS", 5000)
	// Gateway 登录 in-flight 上限（0 表示不限制）。用于控制同时处理的登录请求数。
	GatewayLoginMaxInflight = getenvInt("GATEWAY_LOGIN_MAX_INFLIGHT", 0)
	// 用户密码哈希成本（bcrypt cost，4~31）。值越小越快，但安全性越弱。
	UserPasswordBcryptCost = getenvInt("USER_PASSWORD_BCRYPT_COST", 10)

	KafkaBrokers    = getenv("KAFKA_BROKERS", "localhost:9092")
	KafkaBrokerList = parseCSV(KafkaBrokers)
	// Conversation 消费组：同组实例共同消费；用于 im-message / im-message-read 的水平扩展。
	KafkaConversationIMGroupID   = getenv("KAFKA_CONVERSATION_IM_GROUP_ID", "conversation-im-message")
	KafkaConversationReadGroupID = getenv("KAFKA_CONVERSATION_READ_GROUP_ID", "conversation-im-message-read")
	KafkaConversationPushGroupID = getenv("KAFKA_CONVERSATION_PUSH_GROUP_ID", "conversation-im-message-push")
	// im-message 小批量消费参数：分区内串行，按条数/时间窗触发批处理。
	KafkaConversationIMBatchSize   = getenvInt("KAFKA_CONVERSATION_IM_BATCH_SIZE", 64)
	KafkaConversationIMBatchWaitMs = getenvInt("KAFKA_CONVERSATION_IM_BATCH_WAIT_MS", 5)
	// im-message-push 批消费：减少 Redis 往返与 handler 调度开销（分区内仍串行提交位移）。
	KafkaConversationPushBatchSize   = getenvInt("KAFKA_CONVERSATION_PUSH_BATCH_SIZE", 64)
	KafkaConversationPushBatchWaitMs = getenvInt("KAFKA_CONVERSATION_PUSH_BATCH_WAIT_MS", 3)
	// group-message 消费组与批参数：支持 group-service 多实例同组扩展消费。
	KafkaGroupMessageGroupID     = getenv("KAFKA_GROUP_MESSAGE_GROUP_ID", "group-message-consumer")
	KafkaGroupMemberSyncGroupID  = getenv("KAFKA_GROUP_MEMBER_SYNC_GROUP_ID", "group-member-sync-consumer")
	KafkaGroupMessageBatchSize   = getenvInt("KAFKA_GROUP_MESSAGE_BATCH_SIZE", 64)
	KafkaGroupMessageBatchWaitMs = getenvInt("KAFKA_GROUP_MESSAGE_BATCH_WAIT_MS", 5)
	// group-message 批内并行度：同 group 仍串行保序，不同 group 可并行处理。
	KafkaGroupMessageBatchParallel = getenvInt("KAFKA_GROUP_MESSAGE_BATCH_PARALLEL", 8)
	// group-message -> gateway 批推时，每次 gRPC 请求内最多 items 数，避免单请求过大。
	KafkaGroupPushRPCItemsMax = getenvInt("KAFKA_GROUP_PUSH_RPC_ITEMS_MAX", 2000)
	// group-message 群成员列表本地缓存 TTL（毫秒），用于降低每条群消息都查 DB 成员列表。
	KafkaGroupMemberCacheTTLms = getenvInt("KAFKA_GROUP_MEMBER_CACHE_TTL_MS", 2000)
	// group-message 解码兼容开关：protobuf 优先，JSON 兜底（灰度期可开，稳定后建议关）。
	KafkaGroupMessageJSONFallbackEnabled = getenvBool("KAFKA_GROUP_MESSAGE_JSON_FALLBACK_ENABLED", true)
	// im-message-push 路由本地缓存 TTL（毫秒）。用于减少热用户重复查询 Redis ws:conn，0 表示关闭缓存。
	KafkaConversationPushRouteCacheTTLms = getenvInt("KAFKA_CONVERSATION_PUSH_ROUTE_CACHE_TTL_MS", 1500)
	// 消费端：成功路径打 stdout / 发往 log-topic 极伤吞吐，默认关，仅排障时开。
	KafkaConversationConsumerVerboseLog = getenvBool("KAFKA_CONVERSATION_CONSUMER_VERBOSE_LOG", false)
	// Sarama 拉取与 channel 缓冲：提高高并发下 broker→客户端 吞吐。
	KafkaConsumerChannelBufSize = getenvInt("KAFKA_CONSUMER_CHANNEL_BUFFER_SIZE", 1024)
	KafkaConsumerFetchMaxBytes  = getenvInt("KAFKA_CONSUMER_FETCH_MAX_BYTES", 1048576)
	KafkaConsumerMaxWaitTimeMs  = getenvInt("KAFKA_CONSUMER_MAX_WAIT_TIME_MS", 100)
	// 批消费提交策略：每 N 个成功批次执行一次 Commit，降低高并发下提交开销（崩溃时重复消费窗口变大，幂等兜底）。
	KafkaConsumerCommitEveryBatch = getenvInt("KAFKA_CONSUMER_COMMIT_EVERY_BATCH", 4)
	// Kafka 生产者：true=低延迟（leader ack、无压缩、小批）；false=WaitForAll 全副本确认（更慢、更稳）。
	KafkaProducerLowLatency = getenvBool("KAFKA_PRODUCER_LOW_LATENCY", true)
	// true 时每条成功写入 Kafka 打日志（高 QPS 时极伤延迟，仅排查用）。
	KafkaProducerDebugLog = getenvBool("KAFKA_PRODUCER_DEBUG_LOG", false)
	// Kafka 生产者攒批参数：在“确认后再返回”语义下减少单条 RTT 开销。
	KafkaProducerFlushMessages    = getenvInt("KAFKA_PRODUCER_FLUSH_MESSAGES", 100)
	KafkaProducerFlushFrequencyMs = getenvInt("KAFKA_PRODUCER_FLUSH_FREQUENCY_MS", 5)
	KafkaProducerFlushMaxMessages = getenvInt("KAFKA_PRODUCER_FLUSH_MAX_MESSAGES", 1000)

	MinIOEndpoint  = getenv("MINIO_ENDPOINT", "localhost:19000")
	MinIOAccessKey = getenv("MINIO_ACCESS_KEY", "pimminio")
	MinIOSecretKey = getenv("MINIO_SECRET_KEY", "pimminiopass")
	MinIOBucket    = getenv("MINIO_BUCKET", "pim-files")
	MinIOUseSSL    = getenvBool("MINIO_USE_SSL", false)

	ElasticsearchURL       = getenv("ELASTICSEARCH_URL", "http://localhost:9200")
	LogServiceHTTPURL      = getenv("LOG_SERVICE_HTTP_URL", "http://localhost:9016")
	FileServiceHTTPURL     = getenv("FILE_SERVICE_HTTP_URL", "http://localhost:9006")
	AuthHTTPAddr           = getenv("AUTH_HTTP_ADDR", ":26000")
	AuthGRPCAddr           = getenv("AUTH_GRPC_ADDR", ":26005")
	UserHTTPAddr           = getenv("USER_HTTP_ADDR", ":26001")
	UserGRPCAddr           = getenv("USER_GRPC_ADDR", ":26011")
	FriendHTTPAddr         = getenv("FRIEND_HTTP_ADDR", ":26002")
	FriendGRPCAddr         = getenv("FRIEND_GRPC_ADDR", ":26012")
	FileHTTPAddr           = getenv("FILE_HTTP_ADDR", ":26006")
	FileGRPCAddr           = getenv("FILE_GRPC_ADDR", ":26015")
	LogHTTPAddr            = getenv("LOG_HTTP_ADDR", ":26016")
	GatewayHTTPAddr        = getenv("GATEWAY_HTTP_ADDR", ":26080")
	GatewayPushGRPCAddr    = getenv("GATEWAY_PUSH_GRPC_ADDR", ":26090")
	GatewayNodeID          = getenv("GATEWAY_NODE_ID", "gateway-1")
	ConversationHTTPAddr   = getenv("CONVERSATION_HTTP_ADDR", ":26003")
	ConversationGRPCAddr   = getenv("CONVERSATION_GRPC_ADDR", ":26013")
	GroupHTTPAddr = getenv("GROUP_HTTP_ADDR", ":26004")
	GroupGRPCAddr = getenv("GROUP_GRPC_ADDR", ":26014")

	// etcd：服务注册与 gRPC 发现（唯一路径）。
	EtcdEndpoints   = getenv("ETCD_ENDPOINTS", "127.0.0.1:2379")
	EtcdKeyPrefix   = getenv("ETCD_KEY_PREFIX", "/pim/v1")
	EtcdLeaseTTLSec = getenvInt("ETCD_LEASE_TTL_SEC", 10)
	// SERVICE_INSTANCE_ID 为空时进程内自动生成（多副本建议显式注入稳定 id）。
	ServiceInstanceID = getenv("SERVICE_INSTANCE_ID", "")
	// 网关管理后台聚合探活用的各服务 HTTP /health（集群内需可解析）。
	AdminHealthAuthURL         = getenv("ADMIN_HEALTH_AUTH_URL", "http://localhost:26000/health")
	AdminHealthUserURL         = getenv("ADMIN_HEALTH_USER_URL", "http://localhost:26001/health")
	AdminHealthFriendURL       = getenv("ADMIN_HEALTH_FRIEND_URL", "http://localhost:26002/health")
	AdminHealthConversationURL = getenv("ADMIN_HEALTH_CONVERSATION_URL", "http://localhost:26003/health")
	AdminHealthGroupURL        = getenv("ADMIN_HEALTH_GROUP_URL", "http://localhost:26004/health")
	// 观测聚合使用的 Prometheus 查询地址（用于跨 gateway 实例汇总 QPS）。
	PrometheusQueryURL = getenv("PROMETHEUS_QUERY_URL", "http://localhost:9090")
	LogTopic                   = getenv("LOG_TOPIC", "log-topic")
	LogInfoSamplePct           = clampPct(getenvInt("LOG_INFO_SAMPLE_PCT", 100))

	FilePendingUploadTimeoutSec = getenvInt("FILE_PENDING_UPLOAD_TIMEOUT_SEC", 900)
	FileScanMaxRetry            = getenvInt("FILE_SCAN_MAX_RETRY", 3)
	FileScanRetryBaseSec        = getenvInt("FILE_SCAN_RETRY_BASE_SEC", 1)
	FileScanRetryMaxSec         = getenvInt("FILE_SCAN_RETRY_MAX_SEC", 30)
	FileScanDLQTopic            = getenv("FILE_SCAN_DLQ_TOPIC", "file-scan-dlq")
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}

func getenvBool(key string, def bool) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return b
}

func clampPct(n int) int {
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"localhost:9092"}
	}
	return out
}

// EtcdEndpointList 解析 ETCD_ENDPOINTS（逗号分隔）。
func EtcdEndpointList() []string {
	out := parseCSV(EtcdEndpoints)
	if len(out) == 0 {
		return []string{"127.0.0.1:2379"}
	}
	return out
}

// EffectiveAdvertiseGRPCAddr 供写入 etcd 的对外 gRPC 拨号地址。
func EffectiveAdvertiseGRPCAddr(listenAddr string) string {
	if v := strings.TrimSpace(os.Getenv("SERVICE_ADVERTISE_GRPC_ADDR")); v != "" {
		return v
	}
	if pod := strings.TrimSpace(getenv("POD_IP", "")); pod != "" {
		if p, ok := grpcListenPort(listenAddr); ok {
			return pod + ":" + p
		}
	}
	la := strings.TrimSpace(listenAddr)
	if la == "" {
		return ""
	}
	if strings.HasPrefix(la, ":") {
		return "127.0.0.1" + la
	}
	return la
}

func grpcListenPort(listenAddr string) (string, bool) {
	la := strings.TrimSpace(listenAddr)
	if la == "" {
		return "", false
	}
	if i := strings.LastIndex(la, ":"); i >= 0 && i+1 < len(la) {
		return la[i+1:], true
	}
	return "", false
}

// ServiceInstanceIDOrGenerated 返回稳定或生成的实例 id（用于 etcd 键后缀）。
func ServiceInstanceIDOrGenerated() string {
	if id := strings.TrimSpace(ServiceInstanceID); id != "" {
		return id
	}
	h, _ := os.Hostname()
	return h + "-" + uuid.NewString()[:8]
}

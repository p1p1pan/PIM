package config

import (
	"os"
	"strconv"
	"strings"
)

// 配置信息：环境变量优先，未设置时回落默认值。
var (
	DBHost     = getenv("POSTGRES_HOST", "localhost")
	DBPort     = getenv("POSTGRES_PORT", "5432")
	DBUser     = getenv("POSTGRES_USER", "pim")
	DBPassword = getenv("POSTGRES_PASSWORD", "pim")
	DBName     = getenv("POSTGRES_DB", "pim")

	JWTSecret = getenv("JWT_SECRET", "pim-dev-secret") // JWT 签名密钥

	RedisAddr     = getenv("REDIS_ADDR", "localhost:6379")
	RedisPassword = getenv("REDIS_PASSWORD", "")
	RedisDB       = getenvInt("REDIS_DB", 0)

	KafkaBrokers    = getenv("KAFKA_BROKERS", "localhost:9092")
	KafkaBrokerList = parseCSV(KafkaBrokers)

	MinIOEndpoint  = getenv("MINIO_ENDPOINT", "localhost:19000")
	MinIOAccessKey = getenv("MINIO_ACCESS_KEY", "pimminio")
	MinIOSecretKey = getenv("MINIO_SECRET_KEY", "pimminiopass")
	MinIOBucket    = getenv("MINIO_BUCKET", "pim-files")
	MinIOUseSSL    = getenvBool("MINIO_USE_SSL", false)

	ElasticsearchURL   = getenv("ELASTICSEARCH_URL", "http://localhost:9200")
	LogServiceHTTPURL  = getenv("LOG_SERVICE_HTTP_URL", "http://localhost:9016")
	FileServiceHTTPURL = getenv("FILE_SERVICE_HTTP_URL", "http://localhost:9006")
	LogTopic           = getenv("LOG_TOPIC", "log-topic")
	LogInfoSamplePct   = clampPct(getenvInt("LOG_INFO_SAMPLE_PCT", 100))

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

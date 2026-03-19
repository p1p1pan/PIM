package config

// 配置信息
const (
	DBHost     = "localhost"
	DBPort     = "5432"
	DBUser     = "pim"
	DBPassword = "pim"
	DBName     = "pim"

	JWTSecret = "pim-dev-secret" // JWT 签名密钥

	RedisAddr     = "localhost:6379"
	RedisPassword = ""
	RedisDB       = 0

	KafkaBrokers = "localhost:9092"
)

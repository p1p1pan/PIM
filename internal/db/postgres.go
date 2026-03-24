// Package db 提供各服务共用的 PostgreSQL（GORM）初始化，统一配置 database/sql 连接池。
// 不设分区/分表逻辑；调优通过环境变量（见 internal/config）在部署侧完成。
package db

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/config"
)

// OpenPostgres 使用与 config 一致的 DSN 片段打开 GORM，并对底层 *sql.DB 设置连接池参数。
// 多微服务共用同一 PostgreSQL 时，单实例 MaxOpenConns 不宜过大（各进程之和应小于 max_connections）。
func OpenPostgres() (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost,
		config.DBPort,
		config.DBUser,
		config.DBPassword,
		config.DBName,
	)
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, err
	}
	maxOpen := config.DBMaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 15
	}
	maxIdle := config.DBMaxIdleConns
	if maxIdle < 0 {
		maxIdle = 0
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	if config.DBConnMaxLifetimeSec > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(config.DBConnMaxLifetimeSec) * time.Second)
	}
	if config.DBConnMaxIdleTimeSec > 0 {
		sqlDB.SetConnMaxIdleTime(time.Duration(config.DBConnMaxIdleTimeSec) * time.Second)
	}
	return gdb, nil
}

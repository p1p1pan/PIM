package main

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"

	"pim/internal/config"
	loghandler "pim/internal/log/handler"
	logmq "pim/internal/log/mq"
	observemetrics "pim/internal/kit/observability/metrics"
	logstore "pim/internal/log/store"
)

func main() {
	// 1) 初始化 ES 存储（日志索引与检索）。
	es := logstore.NewESStore(config.ElasticsearchURL)
	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("log-service"))
	observemetrics.RegisterMetricsRoute(r)
	httpServer := loghandler.NewHTTPServer(es)
	httpServer.RegisterRoutes(r)

	// 2) 启动 Kafka 消费协程，持续写入 ES。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logmq.StartConsumers(ctx, es, config.KafkaBrokerList, config.LogTopic)

	// 3) 暴露 log 查询 HTTP API（由 gateway 统一代理给前端）。
	log.Printf("log-service http %s", config.LogHTTPAddr)
	if err := r.Run(config.LogHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"

	"pim/internal/config"
	loghandler "pim/internal/log/handler"
	logmq "pim/internal/log/mq"
	logstore "pim/internal/log/store"
)

func main() {
	es := logstore.NewESStore(config.ElasticsearchURL)
	r := gin.Default()
	httpServer := loghandler.NewHTTPServer(es)
	httpServer.RegisterRoutes(r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logmq.StartConsumers(ctx, es, config.KafkaBrokerList, config.LogTopic)

	log.Println("log-service http :9016")
	if err := r.Run(":9016"); err != nil {
		log.Fatal(err)
	}
}

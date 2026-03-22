package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/config"
	conversationhandler "pim/internal/conversation/handler"
	conversationmodel "pim/internal/conversation/model"
	conversationmq "pim/internal/conversation/mq"
	pbconversation "pim/internal/conversation/pb"
	conversationrepo "pim/internal/conversation/repo"
	conversationservice "pim/internal/conversation/service"
	pbgateway "pim/internal/gateway/pb"
	"pim/internal/mq/kafka"
	observemetrics "pim/internal/observability/metrics"
)

func main() {
	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("conversation-service"))
	observemetrics.RegisterMetricsRoute(r)
	// 1) 初始化 PostgreSQL 并迁移会话/消息相关表。
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost,
		config.DBPort,
		config.DBUser,
		config.DBPassword,
		config.DBName,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&conversationmodel.Message{},
		&conversationmodel.Conversation{},
		&conversationmodel.MessageRead{},
	); err != nil {
		log.Fatalf("Failed to migrate message table: %v", err)
	}
	// 2) 初始化 Redis（未读计数/在线判断）。
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis from conversation-service: %v", err)
	}
	log.Println("conversation-service connected to Redis")

	// 3) 连接 Gateway PushService，负责实时下行。
	pushConn, err := grpc.NewClient("localhost:8090", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to gateway PushService: %v", err)
	}
	defer pushConn.Close()
	pushClient := pbgateway.NewPushServiceClient(pushConn)

	// 4) 启动 Kafka 消费链路（消息落库、已读推进、在线推送）。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conversationSvc := conversationservice.NewService(conversationrepo.NewRepo(db))
	producer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: config.KafkaBrokerList})
	defer producer.Close()

	conversationmq.StartConsumers(ctx, conversationSvc, rdb, pushClient, producer, config.KafkaBrokerList)
	// 5) 启动 gRPC 服务（Conversation 主能力）。
	grpcServer := grpc.NewServer()
	pbconversation.RegisterConversationServiceServer(grpcServer, conversationhandler.NewGRPCConversationServer(conversationSvc))
	listener, err := net.Listen("tcp", ":9013")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// 6) 暴露最小 HTTP（健康检查）。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("conversation-service gRPC :9013, health :9003")
	if err := r.Run(":9003"); err != nil {
		log.Fatal(err)
	}
}

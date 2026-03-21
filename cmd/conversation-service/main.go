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
)

func main() {
	r := gin.Default()
	// database
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
	// redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis from conversation-service: %v", err)
	}
	log.Println("conversation-service connected to Redis")

	// gateway pushservice	内部下行事件 推送到指定用户的WebSocket连接
	pushConn, err := grpc.NewClient("localhost:8090", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to gateway PushService: %v", err)
	}
	defer pushConn.Close()
	pushClient := pbgateway.NewPushServiceClient(pushConn)

	// Kafka Consumer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conversationSvc := conversationservice.NewService(conversationrepo.NewRepo(db))
	producer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: config.KafkaBrokerList})
	defer producer.Close()

	conversationmq.StartConsumers(ctx, conversationSvc, rdb, pushClient, producer, config.KafkaBrokerList)
	// grpc server
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

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("conversation-service gRPC :9013, health :9003")
	if err := r.Run(":9003"); err != nil {
		log.Fatal(err)
	}
}

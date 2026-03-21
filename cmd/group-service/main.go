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
	pbgateway "pim/internal/gateway/pb"
	grouphandler "pim/internal/group/handler"
	groupmodel "pim/internal/group/model"
	groupmq "pim/internal/group/mq"
	pbgroup "pim/internal/group/pb"
	grouprepo "pim/internal/group/repo"
	groupservice "pim/internal/group/service"
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
	if err := db.AutoMigrate(&groupmodel.Group{}, &groupmodel.GroupMember{}, &groupmodel.GroupMessage{}, &groupmodel.GroupReadState{}); err != nil {
		log.Fatalf("Failed to migrate group tables: %v", err)
	}

	// redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	// gateway pushservice	内部下行事件 推送到指定用户的WebSocket连接
	pushConn, err := grpc.NewClient("localhost:8090", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to gateway PushService: %v", err)
	}
	defer pushConn.Close()
	pushClient := pbgateway.NewPushServiceClient(pushConn)

	// grpc server
	groupRepo := grouprepo.NewGroupRepo(db)
	groupSvc := groupservice.NewService(groupRepo)
	grpcServer := grpc.NewServer()
	pbgroup.RegisterGroupServiceServer(grpcServer, grouphandler.NewGRPCGroupServer(groupSvc))

	// Kafka Consumer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	producer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: config.KafkaBrokerList})
	defer producer.Close()
	groupmq.StartConsumers(ctx, groupSvc, rdb, pushClient, producer, config.KafkaBrokerList)

	// listen group grpc
	lis, err := net.Listen("tcp", ":9014")
	if err != nil {
		log.Fatalf("failed to listen group grpc: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve group grpc: %v", err)
		}
	}()

	// http server
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("group-service gRPC :9014, health :9004")
	if err := r.Run(":9004"); err != nil {
		log.Fatal(err)
	}
}

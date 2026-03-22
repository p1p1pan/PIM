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
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/config"
	friendhandler "pim/internal/friend/handler"
	friendmodel "pim/internal/friend/model"
	observemetrics "pim/internal/observability/metrics"
	pbfriend "pim/internal/friend/pb"
	friendrepo "pim/internal/friend/repo"
	friendservice "pim/internal/friend/service"
)

func main() {
	// 1) 初始化 PostgreSQL，承载好友关系与申请状态。
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
	if err := db.AutoMigrate(&friendmodel.Friend{}, &friendmodel.FriendRequest{}, &friendmodel.Blacklist{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}
	// 2) 初始化 Redis（好友列表缓存等高频读场景）。
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer rdb.Close()
	log.Printf("RedisAddr from config: %s", config.RedisAddr)

	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("friend-service"))
	observemetrics.RegisterMetricsRoute(r)
	// 3) 启动 gRPC 服务（Friend 主能力）。
	grpcServer := grpc.NewServer()
	friendSvc := friendservice.NewService(friendrepo.NewFriendRepo(db, rdb))
	pbfriend.RegisterFriendServiceServer(grpcServer, friendhandler.NewGRPCFriendServer(friendSvc))
	listener, err := net.Listen("tcp", ":9012")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	// gRPC 与 HTTP 健康检查分离：gRPC 处理业务，HTTP 仅供探活。
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// 4) 暴露最小 HTTP（健康检查）。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	log.Println("friend-service gRPC :9012, health :9002")
	if err := r.Run(":9002"); err != nil {
		log.Fatal(err)
	}
}

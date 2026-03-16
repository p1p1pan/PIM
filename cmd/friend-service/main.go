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
	"pim/internal/friend"
	pbfriend "pim/internal/friend/pb"
)

func main() {
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
	if err := db.AutoMigrate(&friend.Friend{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
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
	defer rdb.Close()
	log.Printf("RedisAddr from config: %s", config.RedisAddr)

	r := gin.Default()
	// grpc server
	// friend service
	grpcServer := grpc.NewServer()
	pbfriend.RegisterFriendServiceServer(grpcServer, friend.NewGRPCFriendServer(db, rdb))
	listener, err := net.Listen("tcp", ":9012")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	// start grpc server
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// HTTP: 仅健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	log.Println("friend-service gRPC :9012, health :9002")
	if err := r.Run(":9002"); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authhandler "pim/internal/auth/handler"
	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
	pbuser "pim/internal/user/pb"
)

func main() {
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

	// gRPC：仅做 token 校验，不连 DB
	// user service
	userConn, err := grpc.NewClient("localhost:9011", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)

	// auth service
	grpcServer := grpc.NewServer()
	pbauth.RegisterAuthServiceServer(grpcServer, authhandler.NewGRPCAuthServer(userClient))
	listener, err := net.Listen("tcp", ":9005")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("auth-service gRPC :9005, health :9000")
	if err := r.Run(":9000"); err != nil {
		log.Fatal(err)
	}
}

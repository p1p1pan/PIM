package main

import (
	"context"
	"log"
	"net"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pbauth "pim/internal/auth/pb"
	pbconversation "pim/internal/conversation/pb"
	pbfriend "pim/internal/friend/pb"
	gatewayhandler "pim/internal/gateway/handler"
	pbgateway "pim/internal/gateway/pb"
	pbuser "pim/internal/user/pb"

	"pim/internal/config"
)

func main() {
	r := gin.Default()
	r.Use(gatewayhandler.CORSMiddleware())
	r.Use(gatewayhandler.TraceMiddleware())

	redisClient := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer redisClient.Close()
	// 连接 auth service
	authConn, err := grpc.NewClient("localhost:9005", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to auth service: %v", err)
	}
	defer authConn.Close()
	authClient := pbauth.NewAuthServiceClient(authConn)
	// 连接 user service
	userConn, err := grpc.NewClient("localhost:9011", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)
	// 连接 friend service
	friendConn, err := grpc.NewClient("localhost:9012", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to friend service: %v", err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)
	// 连接 conversation service
	conversationConn, err := grpc.NewClient("localhost:9013", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to conversation service: %v", err)
	}
	defer conversationConn.Close()
	conversationClient := pbconversation.NewConversationServiceClient(conversationConn)
	// 创建 http server 用于处理 HTTP 请求
	httpServer := gatewayhandler.NewHTTPServer(
		authClient,
		userClient,
		friendClient,
		conversationClient,
		redisClient,
		[]string{config.KafkaBrokers},
	)
	httpServer.RegisterRoutes(r)

	// 启动 push service 用于处理 WebSocket 连接
	go func() {
		lis, err := net.Listen("tcp", ":8090")
		if err != nil {
			log.Fatalf("Failed to listen for PushService: %v", err)
		}
		grpcServer := grpc.NewServer()
		pbgateway.RegisterPushServiceServer(grpcServer, gatewayhandler.NewPushServiceServer())
		log.Println("Gateway PushService gRPC listening on :8090")
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve PushService gRPC: %v", err)
		}
	}()

	if err := r.Run(":8080"); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

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

	authConn, err := grpc.NewClient("localhost:9005", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to auth service: %v", err)
	}
	defer authConn.Close()
	authClient := pbauth.NewAuthServiceClient(authConn)

	userConn, err := grpc.NewClient("localhost:9011", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)

	friendConn, err := grpc.NewClient("localhost:9012", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to friend service: %v", err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)

	conversationConn, err := grpc.NewClient("localhost:9013", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to conversation service: %v", err)
	}
	defer conversationConn.Close()
	conversationClient := pbconversation.NewConversationServiceClient(conversationConn)

	httpServer := gatewayhandler.NewHTTPServer(
		authClient,
		userClient,
		friendClient,
		conversationClient,
		redisClient,
		[]string{config.KafkaBrokers},
	)
	httpServer.RegisterRoutes(r)

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

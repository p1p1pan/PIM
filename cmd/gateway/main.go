package main

import (
	"context"
	"fmt"
	"log"
	"net"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pbauth "pim/internal/auth/pb"
	pbconversation "pim/internal/conversation/pb"
	pbfile "pim/internal/file/pb"
	pbfriend "pim/internal/friend/pb"
	gatewayhandler "pim/internal/gateway/handler"
	pbgateway "pim/internal/gateway/pb"
	pbgroup "pim/internal/group/pb"
	pbuser "pim/internal/user/pb"

	"pim/internal/config"
	observemetrics "pim/internal/observability/metrics"
)

func main() {
	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(gatewayhandler.CORSMiddleware())
	r.Use(gatewayhandler.TraceMiddleware())
	r.Use(observemetrics.HTTPServerMetricsMiddleware("gateway"))
	observemetrics.RegisterMetricsRoute(r)

	// 1) 初始化 Redis，供在线状态与未读等网关逻辑复用。
	redisClient := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer redisClient.Close()
	// 建立到各后端服务的 gRPC 连接。
	authConn, err := dialGRPC("auth service", "localhost:9005")
	if err != nil {
		log.Fatal(err)
	}
	defer authConn.Close()
	authClient := pbauth.NewAuthServiceClient(authConn)
	userConn, err := dialGRPC("user service", "localhost:9011")
	if err != nil {
		log.Fatal(err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)
	friendConn, err := dialGRPC("friend service", "localhost:9012")
	if err != nil {
		log.Fatal(err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)
	conversationConn, err := dialGRPC("conversation service", "localhost:9013")
	if err != nil {
		log.Fatal(err)
	}
	defer conversationConn.Close()
	conversationClient := pbconversation.NewConversationServiceClient(conversationConn)
	groupConn, err := dialGRPC("group service", config.GroupServiceGRPCTarget)
	if err != nil {
		log.Fatal(err)
	}
	defer groupConn.Close()
	groupClient := pbgroup.NewGroupServiceClient(groupConn)
	fileConn, err := dialGRPC("file service", "localhost:9015")
	if err != nil {
		log.Fatal(err)
	}
	defer fileConn.Close()
	fileClient := pbfile.NewFileServiceClient(fileConn)
	// 创建 HTTP Server 并注入所有下游 gRPC client。
	httpServer := gatewayhandler.NewHTTPServer(
		authClient,
		userClient,
		friendClient,
		conversationClient,
		groupClient,
		fileClient,
		redisClient,
		config.KafkaBrokerList,
		config.GatewayNodeID,
		config.LogServiceHTTPURL,
		config.FileServiceHTTPURL,
	)
	r.Use(httpServer.AccessLogMiddleware())
	defer func() {
		if err := httpServer.Close(); err != nil {
			log.Printf("Failed to close gateway HTTPServer resources: %v", err)
		}
	}()
	httpServer.RegisterRoutes(r)
	gatewayhandler.StartGroupMemberSyncConsumer(context.Background(), config.KafkaBrokerList, config.GatewayNodeID)

	// 2) 启动 PushService gRPC：供 conversation/group 消费端做实时下行。
	go func() {
		lis, err := net.Listen("tcp", config.GatewayPushGRPCAddr)
		if err != nil {
			log.Fatalf("Failed to listen for PushService: %v", err)
		}
		grpcServer := grpc.NewServer()
		pbgateway.RegisterPushServiceServer(grpcServer, gatewayhandler.NewPushServiceServer())
		log.Printf("Gateway PushService gRPC listening on %s (node=%s)", config.GatewayPushGRPCAddr, config.GatewayNodeID)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve PushService gRPC: %v", err)
		}
	}()

	// 3) 启动网关 HTTP 主入口。
	if err := r.Run(config.GatewayHTTPAddr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func dialGRPC(name, target string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", name, err)
	}
	return conn, nil
}

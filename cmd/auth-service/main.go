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
	observemetrics "pim/internal/kit/observability/metrics"
	pbuser "pim/internal/user/pb"
)

func main() {
	// 1) 初始化基础依赖（Redis）。
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer rdb.Close()

	// 2) 连接 user-service，供鉴权时查询用户信息。
	userConn, err := grpc.NewClient(config.UserServiceGRPCTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)

	// 3) 启动 gRPC 服务（Auth 主能力）。
	grpcServer := grpc.NewServer()
	pbauth.RegisterAuthServiceServer(grpcServer, authhandler.NewGRPCAuthServer(userClient))
	listener, err := net.Listen("tcp", config.AuthGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		// gRPC 失败应立即终止进程，避免健康检查误判服务可用。
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// 4) 暴露最小 HTTP（健康检查）。
	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("auth-service"))
	observemetrics.RegisterMetricsRoute(r)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Printf("auth-service gRPC %s, health %s", config.AuthGRPCAddr, config.AuthHTTPAddr)
	if err := r.Run(config.AuthHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

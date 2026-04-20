package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	authhandler "pim/internal/auth/handler"
	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
	grpcresolver "pim/internal/grpcresolver"
	observemetrics "pim/internal/kit/observability/metrics"
	"pim/internal/registry"
	pbuser "pim/internal/user/pb"
)

func main() {
	bg := context.Background()
	cli, err := registry.EtcdClient(bg)
	if err != nil {
		log.Fatalf("etcd: %v", err)
	}
	defer registry.CloseEtcd()

	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(bg).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer rdb.Close()

	userConn, err := grpcresolver.Dial(bg, registry.LogicalUser)
	if err != nil {
		log.Fatalf("Failed to connect to user service: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)

	grpcServer := grpc.NewServer()
	pbauth.RegisterAuthServiceServer(grpcServer, authhandler.NewGRPCAuthServer(userClient))
	listener, err := net.Listen("tcp", config.AuthGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	adv := config.EffectiveAdvertiseGRPCAddr(config.AuthGRPCAddr)
	if adv == "" {
		log.Fatalf("auth advertise grpc addr empty")
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalAuth, iid, registry.EndpointRecord{Addr: adv})
	if err != nil {
		log.Fatalf("etcd register auth: %v", err)
	}
	defer regCloser()

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

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

	"pim/internal/config"
	pimdb "pim/internal/db"
	pbgateway "pim/internal/gateway/pb"
	grouphandler "pim/internal/group/handler"
	groupmodel "pim/internal/group/model"
	groupmq "pim/internal/group/mq"
	pbgroup "pim/internal/group/pb"
	grouprepo "pim/internal/group/repo"
	groupservice "pim/internal/group/service"
	"pim/internal/mq/kafka"
	observemetrics "pim/internal/observability/metrics"
)

func main() {
	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("group-service"))
	observemetrics.RegisterMetricsRoute(r)
	// 1) 初始化 PostgreSQL 并迁移群相关表。
	db, err := pimdb.OpenPostgres()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&groupmodel.Group{}, &groupmodel.GroupMember{}, &groupmodel.GroupMessage{}, &groupmodel.GroupReadState{}); err != nil {
		log.Fatalf("Failed to migrate group tables: %v", err)
	}

	// 2) 初始化 Redis（在线判定等辅助能力）。
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	// 3) 连接 Gateway PushService（支持多 gateway 节点路由）。
	pushTargets := config.ParseGatewayPushTargets(config.GatewayPushGRPCTargets, config.GatewayPushGRPCTarget)
	pushClients := make(map[string]pbgateway.PushServiceClient, len(pushTargets))
	pushConns := make([]*grpc.ClientConn, 0, len(pushTargets))
	for node, target := range pushTargets {
		conn, dialErr := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if dialErr != nil {
			log.Fatalf("Failed to connect to gateway PushService node=%s target=%s: %v", node, target, dialErr)
		}
		pushConns = append(pushConns, conn)
		pushClients[node] = pbgateway.NewPushServiceClient(conn)
	}
	defer func() {
		for _, c := range pushConns {
			_ = c.Close()
		}
	}()

	// 4) 启动 gRPC 服务（Group 主能力）。
	groupRepo := grouprepo.NewGroupRepo(db)
	groupSvc := groupservice.NewService(groupRepo)
	grpcServer := grpc.NewServer()
	pbgroup.RegisterGroupServiceServer(grpcServer, grouphandler.NewGRPCGroupServer(groupSvc))

	// 5) 启动 Kafka 消费链路（group-message 落库与扇出）。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	producer := kafka.NewProducer(kafka.DefaultProducerConfig(config.KafkaBrokerList))
	defer producer.Close()
	groupmq.StartConsumers(ctx, groupSvc, rdb, pushClients, producer, config.KafkaBrokerList)

	// 6) 监听 gRPC 端口。
	lis, err := net.Listen("tcp", config.GroupGRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen group grpc: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve group grpc: %v", err)
		}
	}()

	// 7) 暴露最小 HTTP（健康检查）。
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Printf("group-service gRPC %s, health %s (push targets=%v)", config.GroupGRPCAddr, config.GroupHTTPAddr, pushTargets)
	if err := r.Run(config.GroupHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

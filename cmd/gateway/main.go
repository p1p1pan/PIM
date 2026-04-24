package main

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	pbauth "pim/internal/auth/pb"
	pbconversation "pim/internal/conversation/pb"
	pbfile "pim/internal/file/pb"
	pbfriend "pim/internal/friend/pb"
	gatewayhandler "pim/internal/gateway/handler"
	pbgateway "pim/internal/gateway/pb"
	pbgroup "pim/internal/group/pb"
	pbuser "pim/internal/user/pb"

	"pim/internal/config"
	grpcresolver "pim/internal/grpcresolver"
	observemetrics "pim/internal/kit/observability/metrics"
	"pim/internal/registry"
)

func main() {
	bg := context.Background()
	cli, err := registry.EtcdClient(bg)
	if err != nil {
		log.Fatalf("etcd: %v", err)
	}
	defer registry.CloseEtcd()

	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(gatewayhandler.CORSMiddleware())
	r.Use(gatewayhandler.TraceMiddleware())
	r.Use(observemetrics.HTTPServerMetricsMiddleware("gateway"))
	observemetrics.RegisterMetricsRoute(r)

	redisClient := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := redisClient.Ping(bg).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer redisClient.Close()

	authConn, err := grpcresolver.Dial(bg, registry.LogicalAuth)
	if err != nil {
		log.Fatal(err)
	}
	defer authConn.Close()
	authClient := pbauth.NewAuthServiceClient(authConn)
	userConn, err := grpcresolver.Dial(bg, registry.LogicalUser)
	if err != nil {
		log.Fatal(err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)
	friendConn, err := grpcresolver.Dial(bg, registry.LogicalFriend)
	if err != nil {
		log.Fatal(err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)
	conversationConn, err := grpcresolver.Dial(bg, registry.LogicalConversation)
	if err != nil {
		log.Fatal(err)
	}
	defer conversationConn.Close()
	conversationClient := pbconversation.NewConversationServiceClient(conversationConn)
	groupConn, err := grpcresolver.Dial(bg, registry.LogicalGroup)
	if err != nil {
		log.Fatal(err)
	}
	defer groupConn.Close()
	groupClient := pbgroup.NewGroupServiceClient(groupConn)
	fileConn, err := grpcresolver.Dial(bg, registry.LogicalFile)
	if err != nil {
		log.Fatal(err)
	}
	defer fileConn.Close()
	fileClient := pbfile.NewFileServiceClient(fileConn)

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
	gatewayhandler.StartGroupMemberSyncConsumer(bg, config.KafkaBrokerList, config.GatewayNodeID)

	pushLis, err := net.Listen("tcp", config.GatewayPushGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen for PushService: %v", err)
	}
	adv := config.EffectiveAdvertiseGRPCAddr(config.GatewayPushGRPCAddr)
	if adv == "" {
		log.Fatalf("gateway push advertise addr empty")
	}
	pushCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalGatewayPush, config.GatewayNodeID, registry.EndpointRecord{
		Addr:   adv,
		NodeID: config.GatewayNodeID,
	})
	if err != nil {
		log.Fatalf("etcd register gateway-push: %v", err)
	}
	defer pushCloser()

	go func() {
		// Round 232：10w 连接 + 多 conversation-service 并行批推场景下，
		// 默认 MaxConcurrentStreams=100 会让 gRPC stream 排队，直接把 push P99 拉高。
		// Keepalive 保证空闲 push 通道不被 LB / NAT 静默回收；
		// EnforcementPolicy.MinTime 与 client ping 间隔对齐，避免 GOAWAY(too_many_pings)。
		grpcServer := grpc.NewServer(
			grpc.MaxConcurrentStreams(4096),
			grpc.KeepaliveParams(keepalive.ServerParameters{
				Time:    30 * time.Second,
				Timeout: 10 * time.Second,
			}),
			grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             10 * time.Second,
				PermitWithoutStream: true,
			}),
		)
		pbgateway.RegisterPushServiceServer(grpcServer, gatewayhandler.NewPushServiceServer())
		log.Printf("Gateway PushService gRPC listening on %s (node=%s advertise=%s)", config.GatewayPushGRPCAddr, config.GatewayNodeID, adv)
		if err := grpcServer.Serve(pushLis); err != nil {
			log.Fatalf("Failed to serve PushService: %v", err)
		}
	}()

	if err := r.Run(config.GatewayHTTPAddr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

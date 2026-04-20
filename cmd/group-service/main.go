package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"

	"pim/internal/config"
	grouphandler "pim/internal/group/handler"
	groupmodel "pim/internal/group/model"
	groupmq "pim/internal/group/mq"
	pbgroup "pim/internal/group/pb"
	grouprepo "pim/internal/group/repo"
	groupservice "pim/internal/group/service"
	pimdb "pim/internal/kit/db"
	"pim/internal/kit/mq/kafka"
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
	r.Use(observemetrics.HTTPServerMetricsMiddleware("group-service"))
	observemetrics.RegisterMetricsRoute(r)

	db, err := pimdb.OpenPostgres()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&groupmodel.Group{}, &groupmodel.GroupMember{}, &groupmodel.GroupMessage{}, &groupmodel.GroupReadState{}); err != nil {
		log.Fatalf("Failed to migrate group tables: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(bg).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}

	pushWatch, err := registry.StartGatewayPushWatcher(bg, cli)
	if err != nil {
		log.Fatalf("gateway-push watch: %v", err)
	}
	defer pushWatch.Close()

	groupRepo := grouprepo.NewGroupRepo(db)
	groupSvc := groupservice.NewService(groupRepo)
	grpcServer := grpc.NewServer()
	pbgroup.RegisterGroupServiceServer(grpcServer, grouphandler.NewGRPCGroupServer(groupSvc))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	producer := kafka.NewProducer(kafka.DefaultProducerConfig(config.KafkaBrokerList))
	defer producer.Close()
	groupmq.StartConsumers(ctx, groupSvc, rdb, pushWatch, producer, config.KafkaBrokerList)

	lis, err := net.Listen("tcp", config.GroupGRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen group grpc: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve group grpc: %v", err)
		}
	}()

	adv := config.EffectiveAdvertiseGRPCAddr(config.GroupGRPCAddr)
	if adv == "" {
		log.Fatalf("group advertise grpc addr empty")
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalGroup, iid, registry.EndpointRecord{Addr: adv})
	if err != nil {
		log.Fatalf("etcd register group: %v", err)
	}
	defer regCloser()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Printf("group-service gRPC %s, health %s", config.GroupGRPCAddr, config.GroupHTTPAddr)
	if err := r.Run(config.GroupHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

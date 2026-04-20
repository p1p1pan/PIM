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
	conversationhandler "pim/internal/conversation/handler"
	conversationmodel "pim/internal/conversation/model"
	conversationmq "pim/internal/conversation/mq"
	pbconversation "pim/internal/conversation/pb"
	conversationrepo "pim/internal/conversation/repo"
	conversationservice "pim/internal/conversation/service"
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
	r.Use(observemetrics.HTTPServerMetricsMiddleware("conversation-service"))
	observemetrics.RegisterMetricsRoute(r)

	db, err := pimdb.OpenPostgres()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&conversationmodel.Message{},
		&conversationmodel.Conversation{},
		&conversationmodel.MessageRead{},
	); err != nil {
		log.Fatalf("Failed to migrate message table: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(bg).Err(); err != nil {
		log.Fatalf("Failed to connect to Redis from conversation-service: %v", err)
	}
	log.Println("conversation-service connected to Redis")

	pushWatch, err := registry.StartGatewayPushWatcher(bg, cli)
	if err != nil {
		log.Fatalf("gateway-push watch: %v", err)
	}
	defer pushWatch.Close()

	conversationSvc := conversationservice.NewService(conversationrepo.NewRepo(db))
	producer := kafka.NewProducer(kafka.DefaultProducerConfig(config.KafkaBrokerList))
	defer producer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conversationmq.StartConsumers(ctx, conversationSvc, rdb, pushWatch, producer, config.KafkaBrokerList)

	grpcServer := grpc.NewServer()
	pbconversation.RegisterConversationServiceServer(grpcServer, conversationhandler.NewGRPCConversationServer(conversationSvc))
	listener, err := net.Listen("tcp", config.ConversationGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	adv := config.EffectiveAdvertiseGRPCAddr(config.ConversationGRPCAddr)
	if adv == "" {
		log.Fatalf("conversation advertise grpc addr empty")
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalConversation, iid, registry.EndpointRecord{Addr: adv})
	if err != nil {
		log.Fatalf("etcd register conversation: %v", err)
	}
	defer regCloser()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Printf("conversation-service gRPC %s, health %s", config.ConversationGRPCAddr, config.ConversationHTTPAddr)
	if err := r.Run(config.ConversationHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

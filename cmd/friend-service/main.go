package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"google.golang.org/grpc"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	friendhandler "pim/internal/friend/handler"
	friendmodel "pim/internal/friend/model"
	pbfriend "pim/internal/friend/pb"
	friendrepo "pim/internal/friend/repo"
	friendservice "pim/internal/friend/service"
	grpcresolver "pim/internal/grpcresolver"
	pimdb "pim/internal/kit/db"
	observemetrics "pim/internal/kit/observability/metrics"
	pbuser "pim/internal/user/pb"
	"pim/internal/registry"
)

func main() {
	bg := context.Background()
	cli, err := registry.EtcdClient(bg)
	if err != nil {
		log.Fatalf("etcd: %v", err)
	}
	defer registry.CloseEtcd()

	db, err := pimdb.OpenPostgres()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&friendmodel.Friend{}, &friendmodel.FriendRequest{}, &friendmodel.Blacklist{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := rdb.Ping(bg).Err(); err != nil {
		log.Fatalf("Failed to connect to redis: %v", err)
	}
	defer rdb.Close()
	log.Printf("RedisAddr from config: %s", config.RedisAddr)

	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("friend-service"))
	observemetrics.RegisterMetricsRoute(r)

	userConn, err := grpcresolver.Dial(bg, registry.LogicalUser)
	if err != nil {
		log.Fatalf("dial user: %v", err)
	}
	defer userConn.Close()
	userClient := pbuser.NewUserServiceClient(userConn)

	grpcServer := grpc.NewServer()
	friendSvc := friendservice.NewService(friendrepo.NewFriendRepo(db, rdb), userClient)
	pbfriend.RegisterFriendServiceServer(grpcServer, friendhandler.NewGRPCFriendServer(friendSvc))
	listener, err := net.Listen("tcp", config.FriendGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	adv := config.EffectiveAdvertiseGRPCAddr(config.FriendGRPCAddr)
	if adv == "" {
		log.Fatalf("friend advertise grpc addr empty")
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalFriend, iid, registry.EndpointRecord{Addr: adv})
	if err != nil {
		log.Fatalf("etcd register friend: %v", err)
	}
	defer regCloser()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	log.Printf("friend-service gRPC %s, health %s", config.FriendGRPCAddr, config.FriendHTTPAddr)
	if err := r.Run(config.FriendHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

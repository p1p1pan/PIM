package main

import (
	"context"
	"log"
	"net"
	"net/http"

	"google.golang.org/grpc"

	pbuser "pim/internal/user/pb"

	"github.com/gin-gonic/gin"

	"pim/internal/config"
	pimdb "pim/internal/kit/db"
	observemetrics "pim/internal/kit/observability/metrics"
	"pim/internal/registry"
	userhandler "pim/internal/user/handler"
	usermodel "pim/internal/user/model"
	userrepo "pim/internal/user/repo"
	userservice "pim/internal/user/service"
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
	if err := db.AutoMigrate(&usermodel.User{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}

	grpcServer := grpc.NewServer()
	userSvc := userservice.NewService(userrepo.NewUserRepo(db))
	pbuser.RegisterUserServiceServer(grpcServer, userhandler.NewGRPCUserServer(userSvc))
	listener, err := net.Listen("tcp", config.UserGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	adv := config.EffectiveAdvertiseGRPCAddr(config.UserGRPCAddr)
	if adv == "" {
		log.Fatalf("user advertise grpc addr empty")
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalUser, iid, registry.EndpointRecord{Addr: adv})
	if err != nil {
		log.Fatalf("etcd register user: %v", err)
	}
	defer regCloser()

	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("user-service"))
	observemetrics.RegisterMetricsRoute(r)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Printf("user-service gRPC %s, health %s", config.UserGRPCAddr, config.UserHTTPAddr)
	if err := r.Run(config.UserHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

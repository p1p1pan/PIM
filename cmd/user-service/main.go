package main

import (
	"log"
	"net"
	"net/http"

	"google.golang.org/grpc"

	pbuser "pim/internal/user/pb"

	"github.com/gin-gonic/gin"

	"pim/internal/config"
	pimdb "pim/internal/kit/db"
	observemetrics "pim/internal/kit/observability/metrics"
	userhandler "pim/internal/user/handler"
	usermodel "pim/internal/user/model"
	userrepo "pim/internal/user/repo"
	userservice "pim/internal/user/service"
)

func main() {
	// 1) 初始化 PostgreSQL 与用户表。
	db, err := pimdb.OpenPostgres()
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&usermodel.User{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}

	// 2) 启动 gRPC 服务（User 主能力）。
	grpcServer := grpc.NewServer()
	userSvc := userservice.NewService(userrepo.NewUserRepo(db))
	pbuser.RegisterUserServiceServer(grpcServer, userhandler.NewGRPCUserServer(userSvc))
	listener, err := net.Listen("tcp", config.UserGRPCAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		// gRPC 作为主能力入口，启动失败直接退出。
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// 3) 暴露最小 HTTP（健康检查）。
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

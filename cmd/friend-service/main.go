package main

import (
	"fmt"
	"log"
	"net/http"
	"net"

	"google.golang.org/grpc"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/config"
	"pim/internal/friend"
	pbfriend "pim/internal/friend/pb"
)

func main() {
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost,
		config.DBPort,
		config.DBUser,
		config.DBPassword,
		config.DBName,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&friend.Friend{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}

	r := gin.Default()
	// grpc server
	// friend service
	grpcServer := grpc.NewServer()
	pbfriend.RegisterFriendServiceServer(grpcServer, friend.NewGRPCFriendServer(db))
	listener, err := net.Listen("tcp", ":9012")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	// start grpc server
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// HTTP: 仅健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	log.Println("friend-service gRPC :9012, health :9002")
	if err := r.Run(":9002"); err != nil {
		log.Fatal(err)
	}
}
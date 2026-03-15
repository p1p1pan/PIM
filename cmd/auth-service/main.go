package main

import (
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/auth"
	pbauth "pim/internal/auth/pb"
	"pim/internal/config"
	"pim/internal/user"
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
	if err := db.AutoMigrate(&user.User{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}

	// grpc server
	grpcServer := grpc.NewServer()
	pbauth.RegisterAuthServiceServer(grpcServer, &auth.GRPCAuthServer{})
	listener, err := net.Listen("tcp", ":9005")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("auth-service gRPC :9005, health :9000")
	if err := r.Run(":9000"); err != nil {
		log.Fatal(err)
	}
}

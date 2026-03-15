package main

import (
	"fmt"
	"log"
	"net"
	"net/http"

	"google.golang.org/grpc"

	pbuser "pim/internal/user/pb"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

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
	pbuser.RegisterUserServiceServer(grpcServer, user.NewGRPCUserServer(db))
	listener, err := net.Listen("tcp", ":9011")
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

	log.Println("user-service gRPC :9011, health :9001")
	if err := r.Run(":9001"); err != nil {
		log.Fatal(err)
	}
}

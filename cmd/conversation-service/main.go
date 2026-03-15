package main

import (
	"fmt"
	"log"
	"net/http"
	"net"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"google.golang.org/grpc"
	
	pbconversation "pim/internal/conversation/pb"
	"pim/internal/config"
	"pim/internal/conversation"
)

func main() {
	r := gin.Default()

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
	if err := db.AutoMigrate(&conversation.Message{}); err != nil {
		log.Fatalf("Failed to migrate message table: %v", err)
	}

	// grpc server
	grpcServer := grpc.NewServer()
	pbconversation.RegisterConversationServiceServer(grpcServer, conversation.NewGRPCConversationServer(db))
	listener, err := net.Listen("tcp", ":9013")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("conversation-service gRPC :9013, health :9003")
	if err := r.Run(":9003"); err != nil {
		log.Fatal(err)
	}
}
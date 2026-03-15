package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	authsvc "pim/internal/auth"
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

	authGroup := r.Group("/api/v1", authsvc.AuthMiddleware())
	conversation.RegisterRoutes(r, authGroup, db) 

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("conversation-service listening on :9003")
	if err := r.Run(":9003"); err != nil {
		log.Fatal(err)
	}
}
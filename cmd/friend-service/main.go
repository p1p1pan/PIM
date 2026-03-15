package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/config"
	authsvc "pim/internal/auth"
	"pim/internal/friend"
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
	if err := db.AutoMigrate(&friend.Friend{}); err != nil {
		log.Fatalf("Failed to migrate user table: %v", err)
	}

	authGroup := r.Group("/api/v1", authsvc.AuthMiddleware())
	friend.RegisterRoutes(r, authGroup, db)

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	log.Println("friend-service listening on :9002")
	if err := r.Run(":9002"); err != nil {
		log.Fatal(err)
	}
}
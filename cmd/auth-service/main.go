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

	r.POST("/api/v1/auth/login", func(c *gin.Context) {
		var req user.LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 调用 user-service 的登录接口
		svc := user.NewService(db)
		u, token, err := svc.Login(req.Username, req.Password)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"message": "Login successful",
			"user":    u,
			"token":   token,
		})
	})
	log.Println("auth-service listening on :9000")
	if err := r.Run(":9000"); err != nil {
		log.Fatal(err)
	}
}
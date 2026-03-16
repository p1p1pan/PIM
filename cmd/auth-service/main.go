package main

import (
	"log"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"pim/internal/auth"
	pbauth "pim/internal/auth/pb"
)

func main() {
	// gRPC：仅做 token 校验，不连 DB
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

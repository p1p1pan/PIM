package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"pim/internal/config"
	filehandler "pim/internal/file/handler"
	filemodel "pim/internal/file/model"
	filemq "pim/internal/file/mq"
	pbfile "pim/internal/file/pb"
	filerepo "pim/internal/file/repo"
	fileservice "pim/internal/file/service"
	filestorage "pim/internal/file/storage"
	pbfriend "pim/internal/friend/pb"
	pbgroup "pim/internal/group/pb"
	"pim/internal/mq/kafka"
)

func main() {
	r := gin.Default()
	// 连接 PostgreSQL，并在启动时完成 File 相关表迁移。
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		config.DBHost, config.DBPort, config.DBUser, config.DBPassword, config.DBName,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if err := db.AutoMigrate(&filemodel.File{}, &filemodel.FileScanTask{}); err != nil {
		log.Fatalf("Failed to migrate file tables: %v", err)
	}
	objectStore, err := filestorage.NewStore(
		context.Background(),
		config.MinIOEndpoint,
		config.MinIOAccessKey,
		config.MinIOSecretKey,
		config.MinIOBucket,
		config.MinIOUseSSL,
	)
	if err != nil {
		log.Fatalf("Failed to init minio store: %v", err)
	}

	fileRepo := filerepo.NewFileRepo(db)
	groupConn, err := grpc.NewClient("localhost:9014", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to group service: %v", err)
	}
	defer groupConn.Close()
	groupClient := pbgroup.NewGroupServiceClient(groupConn)
	groupChecker := fileservice.NewGroupGRPCChecker(groupClient)
	friendConn, err := grpc.NewClient("localhost:9012", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to friend service: %v", err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)
	friendChecker := fileservice.NewFriendGRPCChecker(friendClient)
	fileSvc := fileservice.NewService(fileRepo, objectStore, groupChecker, friendChecker)
	// producer 用于 commit 后投递 file-scan 事件。
	producer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: config.KafkaBrokerList})
	defer producer.Close()

	grpcServer := grpc.NewServer()
	pbfile.RegisterFileServiceServer(grpcServer, filehandler.NewGRPCFileServer(fileSvc, producer, objectStore))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 启动 file-scan 消费者（阶段4使用模拟扫描结果）。
	filemq.StartConsumers(ctx, fileSvc, producer, config.KafkaBrokerList)
	// 周期清理超时 pending_upload，避免长期堆积脏记录。
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				timeout := time.Duration(config.FilePendingUploadTimeoutSec) * time.Second
				n, err := fileSvc.ExpirePendingUploads(timeout)
				if err != nil {
					log.Printf("file cleanup failed: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("file cleanup expired pending_upload: %d", n)
				}
			}
		}
	}()

	lis, err := net.Listen("tcp", ":9015")
	if err != nil {
		log.Fatalf("failed to listen file grpc: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve file grpc: %v", err)
		}
	}()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/api/v1/admin/file-scan/dlq", func(c *gin.Context) {
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		tasks, err := fileSvc.ListDLQTasks(limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": tasks, "count": len(tasks)})
	})
	r.POST("/api/v1/admin/file-scan/dlq/:file_id/replay", func(c *gin.Context) {
		fileID64, err := strconv.ParseUint(c.Param("file_id"), 10, 64)
		if err != nil || fileID64 == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file_id"})
			return
		}
		_, err = fileSvc.ReplayDLQTask(uint(fileID64))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		evt := filemodel.FileScanEvent{
			TraceID: fmt.Sprintf("scan-replay-%d", time.Now().UnixNano()),
			FileID:  uint(fileID64),
			Retry:   0,
		}
		data, _ := json.Marshal(evt)
		if err := producer.SendMessage(c.Request.Context(), "file-scan", "", data); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "replayed"})
	})
	log.Println("file-service gRPC :9015, health :9006")
	if err := r.Run(":9006"); err != nil {
		log.Fatal(err)
	}
}

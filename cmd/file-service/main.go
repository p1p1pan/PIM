package main

import (
	"context"
	"fmt"
	"log"
	"net"
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
	observemetrics "pim/internal/observability/metrics"
)

func main() {
	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("file-service"))
	observemetrics.RegisterMetricsRoute(r)
	// 1) 连接 PostgreSQL，并在启动时完成 File 相关表迁移。
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
	// 2) 初始化对象存储（MinIO）。
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

	// 3) 组装 service 依赖（repo + group/friend 权限检查）。
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
	// 4) 初始化 Kafka producer（commit 后投递 file-scan）。
	producer := kafka.NewProducer(&kafka.ProducerConfig{Brokers: config.KafkaBrokerList})
	defer producer.Close()

	// 5) 启动 gRPC 与 HTTP。
	grpcServer := grpc.NewServer()
	pbfile.RegisterFileServiceServer(grpcServer, filehandler.NewGRPCFileServer(fileSvc, producer, objectStore))
	httpServer := filehandler.NewHTTPServer(fileSvc, producer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 6) 启动 file-scan 消费者链路。
	filemq.StartConsumers(ctx, fileSvc, producer, config.KafkaBrokerList)
	// 7) 周期清理超时 pending_upload，避免长期堆积脏记录。
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

	httpServer.RegisterRoutes(r)
	log.Println("file-service gRPC :9015, health :9006")
	if err := r.Run(":9006"); err != nil {
		log.Fatal(err)
	}
}

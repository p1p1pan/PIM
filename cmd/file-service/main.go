package main

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

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
	grpcresolver "pim/internal/grpcresolver"
	pimdb "pim/internal/kit/db"
	"pim/internal/kit/mq/kafka"
	observemetrics "pim/internal/kit/observability/metrics"
	"pim/internal/registry"
)

func main() {
	bg := context.Background()
	cli, err := registry.EtcdClient(bg)
	if err != nil {
		log.Fatalf("etcd: %v", err)
	}
	defer registry.CloseEtcd()

	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(observemetrics.HTTPServerMetricsMiddleware("file-service"))
	observemetrics.RegisterMetricsRoute(r)

	db, err := pimdb.OpenPostgres()
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
	groupConn, err := grpcresolver.Dial(bg, registry.LogicalGroup)
	if err != nil {
		log.Fatalf("Failed to connect to group service: %v", err)
	}
	defer groupConn.Close()
	groupClient := pbgroup.NewGroupServiceClient(groupConn)
	groupChecker := fileservice.NewGroupGRPCChecker(groupClient)
	friendConn, err := grpcresolver.Dial(bg, registry.LogicalFriend)
	if err != nil {
		log.Fatalf("Failed to connect to friend service: %v", err)
	}
	defer friendConn.Close()
	friendClient := pbfriend.NewFriendServiceClient(friendConn)
	friendChecker := fileservice.NewFriendGRPCChecker(friendClient)
	fileSvc := fileservice.NewService(fileRepo, objectStore, groupChecker, friendChecker)
	producer := kafka.NewProducer(kafka.DefaultProducerConfig(config.KafkaBrokerList))
	defer producer.Close()

	grpcServer := grpc.NewServer()
	pbfile.RegisterFileServiceServer(grpcServer, filehandler.NewGRPCFileServer(fileSvc, producer, objectStore))
	httpServer := filehandler.NewHTTPServer(fileSvc, producer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	filemq.StartConsumers(ctx, fileSvc, producer, config.KafkaBrokerList)
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

	lis, err := net.Listen("tcp", config.FileGRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen file grpc: %v", err)
	}
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("failed to serve file grpc: %v", err)
		}
	}()

	adv := config.EffectiveAdvertiseGRPCAddr(config.FileGRPCAddr)
	if adv == "" {
		log.Fatalf("file advertise grpc addr empty")
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalFile, iid, registry.EndpointRecord{Addr: adv})
	if err != nil {
		log.Fatalf("etcd register file: %v", err)
	}
	defer regCloser()

	httpServer.RegisterRoutes(r)
	log.Printf("file-service gRPC %s, health %s", config.FileGRPCAddr, config.FileHTTPAddr)
	if err := r.Run(config.FileHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

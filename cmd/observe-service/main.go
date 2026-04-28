package main

import (
	"context"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	observemetrics "pim/internal/kit/observability/metrics"
	"pim/internal/observe"
	"pim/internal/registry"
)

func main() {
	bg := context.Background()
	cli, err := registry.EtcdClient(bg)
	if err != nil {
		log.Fatalf("etcd: %v", err)
	}
	defer registry.CloseEtcd()

	redisClient := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	if err := redisClient.Ping(bg).Err(); err != nil {
		log.Fatalf("redis: %v", err)
	}
	defer redisClient.Close()

	adv := strings.TrimSpace(config.ServiceAdvertiseHTTPAddr)
	if adv == "" {
		adv = "http://127.0.0.1" + strings.TrimPrefix(config.ObserveHTTPAddr, ":") // best-effort
	}
	if adv == "" {
		adv = "http://127.0.0.1:26280"
	}
	iid := config.ServiceInstanceIDOrGenerated()
	regCloser, err := registry.RegisterEndpoint(bg, cli, registry.LogicalObserve, iid, registry.EndpointRecord{Addr: adv, NodeID: iid})
	if err != nil {
		log.Fatalf("etcd register observe: %v", err)
	}
	defer regCloser()

	r := gin.New()
	observemetrics.UseGinDefaultMiddleware(r)
	r.Use(cors())
	r.Use(observemetrics.HTTPServerMetricsMiddleware("observe-service"))
	observemetrics.RegisterMetricsRoute(r)
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	routeCat := parseRouteCatalog(config.ObserveAPIRouteCatalog)
	svc := &observe.Service{
		LogServiceBaseURL:         strings.TrimRight(config.LogServiceHTTPURL, "/"),
		FileServiceBaseURL:        strings.TrimRight(config.FileServiceHTTPURL, "/"),
		NodeID:                    "observe-service",
		Redis:                     redisClient,
		APIRouteCatalog:           routeCat,
		GatewayMetricsScrapeURL:  strings.TrimSpace(config.ObserveGatewayMetricsScrapeURL),
	}
	observe.RegisterRoutes(svc, r)
	log.Printf("observe-service http %s advertise=%s", config.ObserveHTTPAddr, adv)
	if err := r.Run(config.ObserveHTTPAddr); err != nil {
		log.Fatal(err)
	}
}

func parseRouteCatalog(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Expose-Headers", "X-Trace-Id")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

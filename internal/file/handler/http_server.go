package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	filemodel "pim/internal/file/model"
	fileservice "pim/internal/file/service"
	"pim/internal/kit/mq/kafka"
)

// HTTPServer 提供 file-service 的 HTTP 管理接口。
type HTTPServer struct {
	svc      *fileservice.Service
	producer *kafka.Producer
}

// NewHTTPServer 创建 file-service HTTP 处理器。
func NewHTTPServer(svc *fileservice.Service, producer *kafka.Producer) *HTTPServer {
	return &HTTPServer{svc: svc, producer: producer}
}

// RegisterRoutes 注册 file-service HTTP 路由。
func (s *HTTPServer) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", s.handleHealth)
	r.GET("/api/v1/admin/file-scan/dlq", s.handleListDLQ)
	r.POST("/api/v1/admin/file-scan/dlq/:file_id/replay", s.handleReplayDLQ)
}

func (s *HTTPServer) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *HTTPServer) handleListDLQ(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	tasks, err := s.svc.ListDLQTasks(limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": tasks, "count": len(tasks)})
}

func (s *HTTPServer) handleReplayDLQ(c *gin.Context) {
	fileID64, err := strconv.ParseUint(c.Param("file_id"), 10, 64)
	if err != nil || fileID64 == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file_id"})
		return
	}
	if _, err := s.svc.ReplayDLQTask(uint(fileID64)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	evt := filemodel.FileScanEvent{
		TraceID: fmt.Sprintf("scan-replay-%d", time.Now().UnixNano()),
		FileID:  uint(fileID64),
		Retry:   0,
	}
	data, _ := json.Marshal(evt)
	if err := s.producer.SendMessage(c.Request.Context(), "file-scan", "", data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "replayed"})
}

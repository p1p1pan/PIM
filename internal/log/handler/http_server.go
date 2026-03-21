package handler

import (
	"net/http"
	"strconv"
	"strings"

	logstore "pim/internal/log/store"

	"github.com/gin-gonic/gin"
)

// HTTPServer 提供日志查询接口。
type HTTPServer struct {
	store *logstore.ESStore
}

// NewHTTPServer 创建日志 HTTP server。
func NewHTTPServer(store *logstore.ESStore) *HTTPServer {
	return &HTTPServer{store: store}
}

// RegisterRoutes 注册日志服务路由。
func (s *HTTPServer) RegisterRoutes(r *gin.Engine) {
	r.GET("/health", s.handleHealth)
	r.GET("/api/v1/logs/trace/:trace_id", s.handleTraceLogs)
	r.GET("/api/v1/logs/search", s.handleSearchLogs)
	r.GET("/api/v1/logs/filter", s.handleFilterLogs)
	r.GET("/api/v1/admin/metrics", s.handleAdminMetrics)
}

func (s *HTTPServer) handleHealth(c *gin.Context) {
	if err := s.store.Health(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "down", "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *HTTPServer) handleTraceLogs(c *gin.Context) {
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trace_id is required"})
		return
	}
	size := parseSize(c.Query("size"), 200)
	items, err := s.store.SearchByTraceID(c.Request.Context(), traceID, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "count": len(items)})
}

func (s *HTTPServer) handleSearchLogs(c *gin.Context) {
	eventID := strings.TrimSpace(c.Query("event_id"))
	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}
	size := parseSize(c.Query("size"), 200)
	items, err := s.store.SearchByEventID(c.Request.Context(), eventID, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "count": len(items)})
}

func (s *HTTPServer) handleFilterLogs(c *gin.Context) {
	service := strings.TrimSpace(c.Query("service"))
	level := strings.TrimSpace(c.Query("level"))
	start := strings.TrimSpace(c.Query("start"))
	end := strings.TrimSpace(c.Query("end"))
	size := parseSize(c.Query("size"), 200)

	items, err := s.store.SearchByFilter(c.Request.Context(), service, level, start, end, size)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items":   items,
		"count":   len(items),
		"service": service,
		"level":   level,
		"start":   start,
		"end":     end,
	})
}

func (s *HTTPServer) handleAdminMetrics(c *gin.Context) {
	metrics, err := s.store.AdminMetrics(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, metrics)
}

func parseSize(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || n > 2000 {
		return def
	}
	return n
}

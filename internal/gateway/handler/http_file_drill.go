package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// handleFileScanDLQList 代理 file-service 的死信任务列表。
func (s *HTTPServer) handleFileScanDLQList(c *gin.Context) {
	u := fmt.Sprintf("%s/api/v1/admin/file-scan/dlq?%s", s.fileServiceBaseURL, c.Request.URL.RawQuery)
	s.proxyFileServiceHTTP(c, http.MethodGet, u)
}

// handleFileScanDLQReplay 代理 file-service 的死信重放。
func (s *HTTPServer) handleFileScanDLQReplay(c *gin.Context) {
	id := strings.TrimSpace(c.Param("file_id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_id is required"})
		return
	}
	u := fmt.Sprintf("%s/api/v1/admin/file-scan/dlq/%s/replay", s.fileServiceBaseURL, url.PathEscape(id))
	s.proxyFileServiceHTTP(c, http.MethodPost, u)
}

func (s *HTTPServer) proxyFileServiceHTTP(c *gin.Context, method, target string) {
	req, err := http.NewRequestWithContext(c.Request.Context(), method, target, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "build file-service request failed"})
		return
	}
	if tid := c.GetHeader("X-Trace-Id"); tid != "" {
		req.Header.Set("X-Trace-Id", tid)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "file-service unavailable"})
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read file-service response failed"})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var out map[string]interface{}
		if json.Unmarshal(b, &out) == nil {
			c.JSON(resp.StatusCode, out)
			return
		}
		c.JSON(resp.StatusCode, gin.H{"error": string(b)})
		return
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		c.JSON(http.StatusOK, gin.H{"raw": string(b)})
		return
	}
	c.JSON(http.StatusOK, out)
}

// handleAdminDrillHTTP500 intentionally returns 500 for alert drill.
func (s *HTTPServer) handleAdminDrillHTTP500(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{
		"ok":      false,
		"message": "synthetic 500 for observability drill",
	})
}

// handleAdminDrillLatency sleeps for given milliseconds to simulate latency.
func (s *HTTPServer) handleAdminDrillLatency(c *gin.Context) {
	ms, _ := strconv.Atoi(strings.TrimSpace(c.Query("sleep_ms")))
	if ms <= 0 {
		ms = 350
	}
	if ms > 5000 {
		ms = 5000
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"sleep_ms": ms,
		"message":  "synthetic latency for observability drill",
	})
}

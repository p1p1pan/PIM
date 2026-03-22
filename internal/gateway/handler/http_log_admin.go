package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

type observabilitySnapshot struct {
	at                time.Time
	apiTotals         map[string]float64
	topicProduce      map[string]float64
	topicConsume      map[string]float64
	wsConnectTotal    float64
	wsDisconnectTotal float64
}

var (
	// lastObservabilitySnap 用于计算“本次请求 - 上次请求”的速率快照（qps/rate）。
	// 这是轻量级近似值，目标是让后台页面看到趋势，不替代 Prometheus rate 计算。
	observabilitySnapshotMu sync.Mutex
	lastObservabilitySnap   *observabilitySnapshot
	// 告警 started_at 需要“同一告警态保持稳定”，所以单独维护一个状态机。
	alertStateMu       sync.Mutex
	lastActiveAlertKey string
	lastAlertStartedAt time.Time
)

// handleLogsByTrace 查询 trace_id 对应日志。
func (s *HTTPServer) handleLogsByTrace(c *gin.Context) {
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trace_id is required"})
		return
	}
	size := strings.TrimSpace(c.Query("size"))
	u := fmt.Sprintf("%s/api/v1/logs/trace/%s", s.logServiceBaseURL, url.PathEscape(traceID))
	if size != "" {
		u += "?size=" + url.QueryEscape(size)
	}
	s.proxyLogQuery(c, u)
}

// handleLogsByEvent 查询 event_id 对应日志。
func (s *HTTPServer) handleLogsByEvent(c *gin.Context) {
	eventID := strings.TrimSpace(c.Query("event_id"))
	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "event_id is required"})
		return
	}
	q := url.Values{}
	q.Set("event_id", eventID)
	if size := strings.TrimSpace(c.Query("size")); size != "" {
		q.Set("size", size)
	}
	u := fmt.Sprintf("%s/api/v1/logs/search?%s", s.logServiceBaseURL, q.Encode())
	s.proxyLogQuery(c, u)
}

// handleLogsFilter 按 service/level/time-range 查询日志。
func (s *HTTPServer) handleLogsFilter(c *gin.Context) {
	q := url.Values{}
	if v := strings.TrimSpace(c.Query("service")); v != "" {
		q.Set("service", v)
	}
	if v := strings.TrimSpace(c.Query("level")); v != "" {
		q.Set("level", v)
	}
	if v := strings.TrimSpace(c.Query("start")); v != "" {
		q.Set("start", v)
	}
	if v := strings.TrimSpace(c.Query("end")); v != "" {
		q.Set("end", v)
	}
	if v := strings.TrimSpace(c.Query("size")); v != "" {
		q.Set("size", v)
	}
	u := fmt.Sprintf("%s/api/v1/logs/filter?%s", s.logServiceBaseURL, q.Encode())
	s.proxyLogQuery(c, u)
}

func (s *HTTPServer) proxyLogQuery(c *gin.Context, target string) {
	// 统一由 gateway 代理到 log-service，前端无需直接跨域访问 log-service。
	resp, err := http.Get(target)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "log-service unavailable"})
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read log-service response failed"})
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
	// log-service 约定返回 JSON；若格式异常直接按服务错误处理。
	if err := json.Unmarshal(b, &out); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid log-service response"})
		return
	}
	c.JSON(http.StatusOK, out)
}

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
		// 透传 trace_id，方便跨 gateway/file-service 串联排查。
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

// handleAdminHealth 聚合返回各后端服务健康状态，供管理后台展示。
func (s *HTTPServer) handleAdminHealth(c *gin.Context) {
	type svc struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		URL       string `json:"url"`
		Status    string `json:"status"`
		LatencyMS int64  `json:"latency_ms"`
		Error     string `json:"error,omitempty"`
	}
	targets := []svc{
		{ID: "gateway", Name: "Gateway", URL: "self"},
		{ID: "auth", Name: "Auth Service", URL: "http://localhost:9000/health"},
		{ID: "user", Name: "User Service", URL: "http://localhost:9001/health"},
		{ID: "friend", Name: "Friend Service", URL: "http://localhost:9002/health"},
		{ID: "conversation", Name: "Conversation Service", URL: "http://localhost:9003/health"},
		{ID: "group", Name: "Group Service", URL: "http://localhost:9004/health"},
		{ID: "file", Name: "File Service", URL: s.fileServiceBaseURL + "/health"},
		{ID: "log", Name: "Log Service", URL: s.logServiceBaseURL + "/health"},
	}
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	for i := range targets {
		if targets[i].ID == "gateway" {
			// 网关自身不走 HTTP 回环探测，直接标记为 up。
			targets[i].Status = "up"
			targets[i].LatencyMS = 0
			continue
		}
		start := time.Now()
		resp, err := client.Get(targets[i].URL)
		targets[i].LatencyMS = time.Since(start).Milliseconds()
		if err != nil {
			targets[i].Status = "down"
			targets[i].Error = err.Error()
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			targets[i].Status = "up"
		} else {
			targets[i].Status = "down"
			targets[i].Error = fmt.Sprintf("http %d", resp.StatusCode)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"generated_at": time.Now().Format(time.RFC3339),
		"services":     targets,
	})
}

// handleAdminMetrics 返回后台运行概览指标（在线连接 + 日志统计）。
func (s *HTTPServer) handleAdminMetrics(c *gin.Context) {
	online := int64(0)
	if s.redisClient != nil {
		var cursor uint64
		for {
			// 扫描在线连接键，避免 KEYS 带来的阻塞风险。
			keys, next, err := s.redisClient.Scan(c.Request.Context(), cursor, "ws:conn:*", 500).Result()
			if err != nil {
				break
			}
			online += int64(len(keys))
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}

	metricsURL := strings.TrimRight(s.logServiceBaseURL, "/") + "/api/v1/admin/metrics"
	resp, err := http.Get(metricsURL)
	if err != nil {
		// 日志服务不可用时仍返回在线人数，保证后台页面有降级数据可展示。
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":          "log-service metrics unavailable",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "read metrics response failed",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":          "log-service metrics request failed",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(body, &out); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":          "invalid log-service metrics response",
			"online_users":   online,
			"generated_at":   time.Now().Format(time.RFC3339),
			"log_service_ok": false,
		})
		return
	}
	// 由 gateway 叠加“在线人数”和生成时间，形成统一后台指标响应。
	out["online_users"] = online
	out["log_service_ok"] = true
	out["generated_at"] = time.Now().Format(time.RFC3339)
	c.JSON(http.StatusOK, out)
}

// handleAdminObservabilityOverview returns a stable aggregation contract for frontend dashboards.
func (s *HTTPServer) handleAdminObservabilityOverview(c *gin.Context) {
	serviceHealth, downCount := s.collectServiceHealth(c.Request.Context())
	metricFamilies := gatherMetricFamilies()

	apiQuality := buildAPIQuality(metricFamilies)
	messagePipeline := buildMessagePipeline(metricFamilies)
	connectRate, disconnectRate := fillRateSnapshots(metricFamilies, apiQuality, messagePipeline)
	sloOverview := buildSLOOverview(apiQuality, messagePipeline, downCount, metricFamilies)
	alertsOverview := buildAlertsOverview(downCount, messagePipeline, sloOverview)

	c.JSON(http.StatusOK, gin.H{
		"service_health":   serviceHealth,
		"api_quality":      apiQuality,
		"message_pipeline": messagePipeline,
		"gateway_connections": gin.H{
			"node":               "gateway-1",
			"active_connections": metricGauge(metricFamilies, "pim_gateway_ws_connections"),
			"connect_rate":       connectRate,
			"disconnect_rate":    disconnectRate,
		},
		"alerts_overview": alertsOverview,
		"slo_overview":    sloOverview,
		"generated_at":    time.Now().Format(time.RFC3339),
	})
}

func (s *HTTPServer) collectServiceHealth(ctx context.Context) ([]gin.H, int) {
	type svc struct {
		ID  string
		URL string
	}
	targets := []svc{
		{ID: "gateway", URL: "self"},
		{ID: "auth", URL: "http://localhost:9000/health"},
		{ID: "user", URL: "http://localhost:9001/health"},
		{ID: "friend", URL: "http://localhost:9002/health"},
		{ID: "conversation", URL: "http://localhost:9003/health"},
		{ID: "group", URL: "http://localhost:9004/health"},
		{ID: "file", URL: s.fileServiceBaseURL + "/health"},
		{ID: "log", URL: s.logServiceBaseURL + "/health"},
	}
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	items := make([]gin.H, 0, len(targets))
	downCount := 0
	for _, t := range targets {
		item := gin.H{
			"service":        t.ID,
			"up":             true,
			"error_rate":     0.0,
			"p95_latency_ms": 0,
		}
		if t.ID == "gateway" {
			items = append(items, item)
			continue
		}
		start := time.Now()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
		resp, err := client.Do(req)
		latency := time.Since(start).Milliseconds()
		item["p95_latency_ms"] = latency
		if err != nil || resp == nil {
			item["up"] = false
			item["error_rate"] = 1.0
			downCount++
			items = append(items, item)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			item["up"] = false
			item["error_rate"] = 1.0
			downCount++
		}
		items = append(items, item)
	}
	return items, downCount
}

func gatherMetricFamilies() map[string]*dto.MetricFamily {
	out := map[string]*dto.MetricFamily{}
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return out
	}
	for _, mf := range mfs {
		if mf == nil || mf.Name == nil {
			continue
		}
		out[mf.GetName()] = mf
	}
	return out
}

func buildAPIQuality(mfs map[string]*dto.MetricFamily) []gin.H {
	type key struct {
		service string
		route   string
	}
	totalByKey := map[key]float64{}
	errByKey := map[key]float64{}
	mf := mfs["pim_http_requests_total"]
	if mf != nil {
		for _, metric := range mf.Metric {
			svc := labelValue(metric, "service")
			route := labelValue(metric, "route")
			status := labelValue(metric, "status")
			k := key{service: svc, route: route}
			v := metric.GetCounter().GetValue()
			totalByKey[k] += v
			if strings.HasPrefix(status, "4") || strings.HasPrefix(status, "5") {
				errByKey[k] += v
			}
		}
	}
	keys := make([]key, 0, len(totalByKey))
	for k := range totalByKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].service == keys[j].service {
			return keys[i].route < keys[j].route
		}
		return keys[i].service < keys[j].service
	})
	out := make([]gin.H, 0, len(keys))
	for _, k := range keys {
		total := totalByKey[k]
		errs := errByKey[k]
		errorRate := 0.0
		if total > 0 {
			errorRate = errs / total
		}
		out = append(out, gin.H{
			"service":        k.service,
			"route":          k.route,
			"qps":            0, // stable contract; actual rate is computed in Prometheus/Grafana.
			"error_rate":     errorRate,
			"p95_latency_ms": 0,
		})
	}
	return out
}

func buildMessagePipeline(mfs map[string]*dto.MetricFamily) []gin.H {
	type counters struct {
		produceOk float64
		consumeOk float64
		dlq       float64
		retryLike float64
	}
	byTopic := map[string]*counters{}
	mf := mfs["pim_kafka_messages_total"]
	if mf != nil {
		for _, metric := range mf.Metric {
			topic := labelValue(metric, "topic")
			direction := labelValue(metric, "direction")
			result := labelValue(metric, "result")
			if topic == "" {
				continue
			}
			c := byTopic[topic]
			if c == nil {
				c = &counters{}
				byTopic[topic] = c
			}
			val := metric.GetCounter().GetValue()
			if direction == "produce" && result == "ok" {
				c.produceOk += val
			}
			if direction == "consume" && result == "ok" {
				c.consumeOk += val
			}
			if topic == "file-scan-dlq" && direction == "produce" && result == "ok" {
				c.dlq += val
			}
			if result == "handler_error" {
				c.retryLike += val
			}
		}
	}
	topics := make([]string, 0, len(byTopic))
	for t := range byTopic {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	out := make([]gin.H, 0, len(topics))
	for _, t := range topics {
		c := byTopic[t]
		out = append(out, gin.H{
			"topic":        t,
			"produce_rate": 0,
			"consume_rate": 0,
			"retry_count":  int64(c.retryLike),
			"dlq_count":    int64(c.dlq),
			"e2e_p95_ms":   estimateKafkaTopicP95MS(mfs, t),
			// Snapshot counters help frontend show trend deltas without coupling to raw metrics names.
			"produce_total": int64(c.produceOk),
			"consume_total": int64(c.consumeOk),
		})
	}
	return out
}

func buildSLOOverview(apiQuality []gin.H, messagePipeline []gin.H, downCount int, mfs map[string]*dto.MetricFamily) gin.H {
	totalErrorRate := 0.0
	samples := 0.0
	for _, item := range apiQuality {
		route, _ := item["route"].(string)
		// 监督口径不把管理/演练接口计入业务 SLO，避免 drill 流量长期污染可用性。
		if strings.HasPrefix(route, "/api/v1/admin/") {
			continue
		}
		v, ok := item["error_rate"].(float64)
		if !ok {
			continue
		}
		totalErrorRate += v
		samples++
	}
	avgErrorRate := 0.0
	if samples > 0 {
		avgErrorRate = totalErrorRate / samples
	}
	availability := 1.0 - avgErrorRate
	if downCount > 0 {
		availability = 0.0
	}
	if availability < 0 {
		availability = 0
	}
	availability = math.Round(availability*10000) / 10000

	apiTarget := 0.99
	apiMet := availability >= apiTarget
	apiP95Now := estimateHTTPP95MS(mfs)
	apiP95Met := apiP95Now > 0 && apiP95Now <= 300
	msgP95Now := estimateMessageE2EP95MS(messagePipeline)
	msgP95Met := msgP95Now > 0 && msgP95Now <= 500
	return gin.H{
		"api_availability_target": apiTarget,
		"api_availability_now":    availability,
		"api_availability_met":    apiMet,
		"api_p95_target_ms":       300,
		"api_p95_now_ms":          apiP95Now,
		"api_p95_met":             apiP95Met,
		"msg_e2e_p95_target_ms":   500,
		"msg_e2e_p95_now_ms":      msgP95Now,
		"msg_e2e_p95_met":         msgP95Met,
	}
}

func buildAlertsOverview(downCount int, messagePipeline []gin.H, slo gin.H) gin.H {
	activeCount := 0
	severity := "none"
	source := "none"

	if downCount > 0 {
		activeCount++
		severity = "critical"
		source = "service_health"
	}
	for _, item := range messagePipeline {
		if dlq, ok := item["dlq_count"].(int64); ok && dlq > 0 {
			activeCount++
			if severity != "critical" {
				severity = "warning"
				source = "dlq"
			}
		}
		if retry, ok := item["retry_count"].(int64); ok && retry > 0 {
			activeCount++
			if severity == "none" {
				severity = "warning"
				source = "retry"
			}
		}
	}
	if met, ok := slo["api_availability_met"].(bool); ok && !met {
		activeCount++
		if severity == "none" {
			severity = "warning"
			source = "slo"
		}
	}
	if met, ok := slo["msg_e2e_p95_met"].(bool); ok && !met {
		activeCount++
		if severity == "none" {
			severity = "warning"
			source = "slo"
		}
	}

	startedAt := stableAlertStartedAt(activeCount, severity, source)

	return gin.H{
		"active_count": activeCount,
		"severity":     severity,
		"source":       source,
		"started_at":   startedAt,
	}
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

func metricGauge(mfs map[string]*dto.MetricFamily, name string) int64 {
	mf := mfs[name]
	if mf == nil || len(mf.Metric) == 0 {
		return 0
	}
	return int64(mf.Metric[0].GetGauge().GetValue())
}

func labelValue(metric *dto.Metric, key string) string {
	for _, lp := range metric.GetLabel() {
		if lp.GetName() == key {
			return lp.GetValue()
		}
	}
	return ""
}

func fillRateSnapshots(mfs map[string]*dto.MetricFamily, apiQuality, messagePipeline []gin.H) (float64, float64) {
	now := time.Now()
	current := &observabilitySnapshot{
		at:                now,
		apiTotals:         buildAPITotalSnapshots(mfs),
		topicProduce:      map[string]float64{},
		topicConsume:      map[string]float64{},
		wsConnectTotal:    metricCounter(mfs, "pim_gateway_ws_connections_total"),
		wsDisconnectTotal: metricCounter(mfs, "pim_gateway_ws_disconnects_total"),
	}
	for _, item := range messagePipeline {
		topic, _ := item["topic"].(string)
		if topic == "" {
			continue
		}
		produce, _ := item["produce_total"].(int64)
		consume, _ := item["consume_total"].(int64)
		current.topicProduce[topic] = float64(produce)
		current.topicConsume[topic] = float64(consume)
	}

	// 先交换快照，再基于 old/new 做差，避免并发请求导致读写交叉。
	observabilitySnapshotMu.Lock()
	prev := lastObservabilitySnap
	lastObservabilitySnap = current
	observabilitySnapshotMu.Unlock()

	if prev == nil || now.Sub(prev.at) <= 0 {
		return 0, 0
	}
	secs := now.Sub(prev.at).Seconds()
	if secs <= 0 {
		return 0, 0
	}

	for _, item := range apiQuality {
		service, _ := item["service"].(string)
		route, _ := item["route"].(string)
		k := service + "|" + route
		cur := current.apiTotals[k]
		prevVal := prev.apiTotals[k]
		delta := cur - prevVal
		if delta < 0 {
			delta = 0
		}
		item["qps"] = math.Round((delta/secs)*1000) / 1000
	}

	for _, item := range messagePipeline {
		topic, _ := item["topic"].(string)
		if topic == "" {
			continue
		}
		curProduce := current.topicProduce[topic]
		curConsume := current.topicConsume[topic]
		prevProduce := prev.topicProduce[topic]
		prevConsume := prev.topicConsume[topic]
		deltaP := curProduce - prevProduce
		deltaC := curConsume - prevConsume
		if deltaP < 0 {
			deltaP = 0
		}
		if deltaC < 0 {
			deltaC = 0
		}
		item["produce_rate"] = math.Round((deltaP/secs)*1000) / 1000
		item["consume_rate"] = math.Round((deltaC/secs)*1000) / 1000
	}

	connectRate := current.wsConnectTotal - prev.wsConnectTotal
	disconnectRate := current.wsDisconnectTotal - prev.wsDisconnectTotal
	if connectRate < 0 {
		connectRate = 0
	}
	if disconnectRate < 0 {
		disconnectRate = 0
	}
	return math.Round((connectRate/secs)*1000) / 1000, math.Round((disconnectRate/secs)*1000) / 1000
}

func buildAPITotalSnapshots(mfs map[string]*dto.MetricFamily) map[string]float64 {
	out := map[string]float64{}
	mf := mfs["pim_http_requests_total"]
	if mf == nil {
		return out
	}
	for _, metric := range mf.Metric {
		service := labelValue(metric, "service")
		route := labelValue(metric, "route")
		if service == "" || route == "" {
			continue
		}
		k := service + "|" + route
		out[k] += metric.GetCounter().GetValue()
	}
	return out
}

func metricCounter(mfs map[string]*dto.MetricFamily, name string) float64 {
	mf := mfs[name]
	if mf == nil || len(mf.Metric) == 0 {
		return 0
	}
	total := 0.0
	for _, m := range mf.Metric {
		total += m.GetCounter().GetValue()
	}
	return total
}

func estimateHTTPP95MS(mfs map[string]*dto.MetricFamily) int64 {
	mf := mfs["pim_http_request_duration_seconds"]
	if mf == nil {
		return 0
	}
	// Aggregate all non-admin routes into one histogram snapshot.
	byLE := map[float64]float64{}
	for _, metric := range mf.Metric {
		route := labelValue(metric, "route")
		if strings.HasPrefix(route, "/api/v1/admin/") {
			continue
		}
		h := metric.GetHistogram()
		if h == nil {
			continue
		}
		for _, b := range h.GetBucket() {
			byLE[b.GetUpperBound()] += float64(b.GetCumulativeCount())
		}
	}
	if len(byLE) == 0 {
		return 0
	}
	les := make([]float64, 0, len(byLE))
	for le := range byLE {
		les = append(les, le)
	}
	sort.Float64s(les)
	total := byLE[les[len(les)-1]]
	if total <= 0 {
		return 0
	}
	target := total * 0.95
	prevCount := 0.0
	prevLE := 0.0
	for _, le := range les {
		cur := byLE[le]
		if cur >= target {
			if le == prevLE || cur <= prevCount {
				return int64(math.Round(le * 1000))
			}
			ratio := (target - prevCount) / (cur - prevCount)
			v := prevLE + ratio*(le-prevLE)
			if v < 0 {
				v = 0
			}
			return int64(math.Round(v * 1000))
		}
		prevCount = cur
		prevLE = le
	}
	return int64(math.Round(les[len(les)-1] * 1000))
}

func estimateKafkaTopicP95MS(mfs map[string]*dto.MetricFamily, topic string) int64 {
	mf := mfs["pim_kafka_handler_duration_seconds"]
	if mf == nil {
		return 0
	}
	byLE := map[float64]float64{}
	for _, metric := range mf.Metric {
		if labelValue(metric, "topic") != topic {
			continue
		}
		// 仅统计成功路径，避免失败重试把“正常处理时延”口径拉偏。
		if labelValue(metric, "result") != "ok" {
			continue
		}
		h := metric.GetHistogram()
		if h == nil {
			continue
		}
		for _, b := range h.GetBucket() {
			byLE[b.GetUpperBound()] += float64(b.GetCumulativeCount())
		}
	}
	if len(byLE) == 0 {
		return 0
	}
	les := make([]float64, 0, len(byLE))
	for le := range byLE {
		les = append(les, le)
	}
	sort.Float64s(les)
	total := byLE[les[len(les)-1]]
	if total <= 0 {
		return 0
	}
	target := total * 0.95
	prevCount := 0.0
	prevLE := 0.0
	for _, le := range les {
		cur := byLE[le]
		if cur >= target {
			if le == prevLE || cur <= prevCount {
				return int64(math.Round(le * 1000))
			}
			ratio := (target - prevCount) / (cur - prevCount)
			v := prevLE + ratio*(le-prevLE)
			if v < 0 {
				v = 0
			}
			return int64(math.Round(v * 1000))
		}
		prevCount = cur
		prevLE = le
	}
	return int64(math.Round(les[len(les)-1] * 1000))
}

func estimateMessageE2EP95MS(messagePipeline []gin.H) int64 {
	// Topic 粒度 p95 先取最大值作为总体消息链路风险快照，避免被低负载 topic 平滑掉。
	maxP95 := int64(0)
	for _, item := range messagePipeline {
		v, ok := item["e2e_p95_ms"].(int64)
		if !ok {
			continue
		}
		if v > maxP95 {
			maxP95 = v
		}
	}
	return maxP95
}

func stableAlertStartedAt(activeCount int, severity, source string) string {
	if activeCount <= 0 || severity == "none" {
		alertStateMu.Lock()
		lastActiveAlertKey = ""
		lastAlertStartedAt = time.Time{}
		alertStateMu.Unlock()
		return ""
	}
	key := fmt.Sprintf("%s|%s|%d", severity, source, activeCount)
	now := time.Now()
	alertStateMu.Lock()
	defer alertStateMu.Unlock()
	// 只有告警状态发生变化时才重置开始时间，保证 started_at 可用于“持续时长”展示。
	// 这里的“状态”使用 severity/source/count 近似表达，满足当前后台聚合展示需求。
	if key != lastActiveAlertKey || lastAlertStartedAt.IsZero() {
		lastActiveAlertKey = key
		lastAlertStartedAt = now
	}
	return lastAlertStartedAt.Format(time.RFC3339)
}

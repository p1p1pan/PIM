// Package observe 提供独立 observe 服务的运行概览、日志与后台 metrics；从 gateway 拆出。
package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/redis/go-redis/v9"

	"pim/internal/config"
	"pim/internal/observability/promql"
)

// Service 承载与 gateway.HTTPServer 中已迁出的只读 admin 能力，不含 drill / 文件死信重放写操作。
type Service struct {
	LogServiceBaseURL  string
	FileServiceBaseURL string
	// NodeID 用于在响应中自描述（如 observe-service-1），gateway_connections 优先走 Prom 聚合。
	NodeID string
	// Redis 用于 /admin/metrics 在线连接扫描（与迁出前 gateway 行为一致）。
	Redis *redis.Client
	// APIRouteCatalog 与 gateway 的 apiRouteCatalog 一致，未配置时使用 DefaultAPIRouteCatalog。
	APIRouteCatalog []string
	// GatewayMetricsScrapeURL 若设（如 http://127.0.0.1:26080/metrics），用文本格式解析为本地 mfs 快照以降级。
	GatewayMetricsScrapeURL string
}

type observabilitySnapshot struct {
	at                time.Time
	apiTotals         map[string]float64
	topicProduce      map[string]float64
	topicConsume      map[string]float64
	wsConnectTotal    float64
	wsDisconnectTotal float64
}

type metricPoint struct {
	TS  time.Time
	Val float64
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
	seriesMu           sync.Mutex
	metricSeriesStore  = map[string][]metricPoint{}
)

// handleLogsByTrace 查询 trace_id 对应日志。
func (s *Service) handleLogsByTrace(c *gin.Context) {
	traceID := strings.TrimSpace(c.Param("trace_id"))
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trace_id is required"})
		return
	}
	size := strings.TrimSpace(c.Query("size"))
	u := fmt.Sprintf("%s/api/v1/logs/trace/%s", s.LogServiceBaseURL, url.PathEscape(traceID))
	if size != "" {
		u += "?size=" + url.QueryEscape(size)
	}
	s.proxyLogQuery(c, u)
}

// handleLogsByEvent 查询 event_id 对应日志。
func (s *Service) handleLogsByEvent(c *gin.Context) {
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
	u := fmt.Sprintf("%s/api/v1/logs/search?%s", s.LogServiceBaseURL, q.Encode())
	s.proxyLogQuery(c, u)
}

// handleLogsFilter 按 service/level/time-range 查询日志。
func (s *Service) handleLogsFilter(c *gin.Context) {
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
	u := fmt.Sprintf("%s/api/v1/logs/filter?%s", s.LogServiceBaseURL, q.Encode())
	s.proxyLogQuery(c, u)
}

func (s *Service) proxyLogQuery(c *gin.Context, target string) {
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

// handleAdminHealth 聚合返回各后端服务健康状态，供管理后台展示。
func (s *Service) handleAdminHealth(c *gin.Context) {
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
		{ID: "auth", Name: "Auth Service", URL: config.AdminHealthAuthURL},
		{ID: "user", Name: "User Service", URL: config.AdminHealthUserURL},
		{ID: "friend", Name: "Friend Service", URL: config.AdminHealthFriendURL},
		{ID: "conversation", Name: "Conversation Service", URL: config.AdminHealthConversationURL},
		{ID: "group", Name: "Group Service", URL: config.AdminHealthGroupURL},
		{ID: "file", Name: "File Service", URL: s.FileServiceBaseURL + "/health"},
		{ID: "log", Name: "Log Service", URL: s.LogServiceBaseURL + "/health"},
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
func (s *Service) handleAdminMetrics(c *gin.Context) {
	online := int64(0)
	if s.Redis != nil {
		var cursor uint64
		for {
			// 扫描在线连接键，避免 KEYS 带来的阻塞风险。
			keys, next, err := s.Redis.Scan(c.Request.Context(), cursor, "ws:conn:*", 500).Result()
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

	metricsURL := strings.TrimRight(s.LogServiceBaseURL, "/") + "/api/v1/admin/metrics"
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

// metricFamiliesHasGatewayScrapeContent 为 true 表示 mfs 中含仅网关注册的系列（非 observe 本进程 HTTP 的那套）。
// 用于 meta：避免用 pim_http_requests_total 误判（observe 也有同名、service=observe-service）。
func metricFamiliesHasGatewayScrapeContent(mfs map[string]*dto.MetricFamily) bool {
	for _, n := range []string{
		"pim_gateway_ws_connections",
		"pim_gateway_push_total",
		"pim_gateway_ws_send_duration_seconds",
	} {
		mf := mfs[n]
		if mf == nil {
			continue
		}
		if len(mf.Metric) > 0 {
			return true
		}
	}
	return false
}

// handleAdminObservabilityOverview returns a stable aggregation contract for frontend dashboards.
//
// 数据来源混合：
//   - 集群级（KPI、API 表格、Topic 速率、SLO）优先走 PromQL 聚合，避免 gateway-1/2 切换引起跳变；
//   - 按实例视角（gateway_connections、service_health）保留本地 gather + 本机探测，
//     因为集群聚合对这类指标反而失去定位意义。
//
// 每一个走 Prom 的指标块都带本地 fallback；最终在 sources/scopes 里如实暴露给前端。
func (s *Service) handleAdminObservabilityOverview(c *gin.Context) {
	ctx := c.Request.Context()

	// 默认所有块标为 local；若对应 Prom 查询成功，覆盖为 prom。
	sources := gin.H{
		"ingress_qps":         "local",
		"api_p95_ms":          "local",
		"error_rate":          "local",
		"push_success_rate":   "local",
		"kafka_retry_total":   "local",
		"api_quality":         "local",
		"message_pipeline":    "local",
		"slo_overview":        "local",
		"gateway_connections": "local",
		"service_health":      "local",
		"service_instances":   "local",
		"timeseries":          "local",
	}
	// scope 表示聚合视角：cluster=所有实例聚合；local=当前网关自报。
	scopes := gin.H{
		"ingress_qps":         "cluster",
		"api_p95_ms":          "cluster",
		"error_rate":          "cluster",
		"push_success_rate":   "cluster",
		"kafka_retry_total":   "cluster",
		"api_quality":         "cluster",
		"message_pipeline":    "cluster",
		"slo_overview":        "cluster",
		"gateway_connections": "local",
		"service_health":      "local",
		"service_instances":   "cluster",
		"timeseries":          "cluster",
	}

	serviceHealth, downCount := s.collectServiceHealth(ctx)
	metricFamilies, gatewayMetricsScraped := s.gatherMetricFamilies()

	// 集群级按实例视图：只有 Prom 可用时才填充；否则前端只显示本地 service_health 摘要。
	serviceInstances, svcUp, svcTotal, svcInstOK := collectServiceInstancesFromProm(ctx)
	if svcInstOK {
		sources["service_instances"] = "prom"
	}

	apiQuality := buildAPIQuality(metricFamilies)
	routeCat := s.APIRouteCatalog
	if len(routeCat) == 0 {
		routeCat = DefaultAPIRouteCatalog()
	}
	apiQuality = mergeAPIQualityWithRouteCatalog(apiQuality, routeCat)
	messagePipeline := buildMessagePipeline(metricFamilies)
	connectRate, disconnectRate := fillRateSnapshots(metricFamilies, apiQuality, messagePipeline)

	// 用集群 Prom 数据覆盖到 route/topic 明细表上。覆盖失败则保留本地快照值。
	if overlayAPIQualityFromProm(ctx, apiQuality) {
		sources["api_quality"] = "prom"
	}
	if overlayTopicRatesFromProm(ctx, messagePipeline) {
		sources["message_pipeline"] = "prom"
	}

	windowDur := parseOverviewWindow(c.Query("window"))
	appendAPITimeseries(apiQuality)

	ingressQPS := computeIngressQPS(apiQuality)
	if v, ok := computeIngressQPSFromProm(ctx); ok {
		ingressQPS = v
		sources["ingress_qps"] = "prom"
	}

	// sloOverview 基于已被 Prom overlay 的 apiQuality/messagePipeline 推导；
	// 若能从 Prom 拿到更准的全局 p95，单独再覆盖 api_p95_now_ms 与达标判定。
	sloOverview := buildSLOOverview(apiQuality, messagePipeline, downCount, metricFamilies)
	if v, ok := computeAPIP95MSFromProm(ctx); ok {
		sloOverview["api_p95_now_ms"] = v
		sloOverview["api_p95_met"] = v > 0 && v <= 300
		sources["api_p95_ms"] = "prom"
		sources["slo_overview"] = "prom"
	}
	// 优先用新增的 pim_im_e2e_server_seconds（直接对照 bench.e2e 的服务端段）。
	// 没流量或 Prom 不可用时保留 estimateMessageE2EP95MS 的本地旧口径。
	if v, ok := computeMsgE2EP95MSFromProm(ctx); ok {
		sloOverview["msg_e2e_p95_now_ms"] = v
		sloOverview["msg_e2e_p95_met"] = v > 0 && v <= 500
		sources["slo_overview"] = "prom"
	}
	// WS ack p95（对照 bench.ack 的服务端段），只在能拿到 Prom 数据时展示。
	if v, ok := computeWSAckP95MSFromProm(ctx); ok {
		sloOverview["ws_ack_p95_now_ms"] = v
		sloOverview["ws_ack_p95_target_ms"] = 100
		sloOverview["ws_ack_p95_met"] = v > 0 && v <= 100
	}
	if v, ok := computeWSAckP99MSFromProm(ctx); ok {
		sloOverview["ws_ack_p99_now_ms"] = v
		sources["slo_overview"] = "prom"
	}
	if v, ok := computeMsgE2EP99MSFromProm(ctx); ok {
		sloOverview["msg_e2e_p99_now_ms"] = v
		sources["slo_overview"] = "prom"
	}

	benchIM := gin.H{}
	if v, _, ok := promql.Scalar(ctx, `sum(rate(pim_gateway_ws_send_duration_seconds_count[1m]))`); ok {
		benchIM["ws_ack_obs_per_sec"] = math.Round(v*1000) / 1000
	}
	if v, ok := computeWSAckP95MSFromProm(ctx); ok {
		benchIM["ws_ack_p95_ms"] = v
	}
	if v, ok := computeWSAckP99MSFromProm(ctx); ok {
		benchIM["ws_ack_p99_ms"] = v
	}
	if v, ok := computeMsgE2EP95MSFromProm(ctx); ok {
		benchIM["im_e2e_p95_ms"] = v
	}
	if v, ok := computeMsgE2EP99MSFromProm(ctx); ok {
		benchIM["im_e2e_p99_ms"] = v
	}
	if v, _, ok := promql.Scalar(ctx, `sum(rate(pim_im_e2e_server_seconds_count[1m]))`); ok {
		benchIM["im_e2e_obs_per_sec"] = math.Round(v*1000) / 1000
	}

	nonAdminErrorRate := computeNonAdminErrorRate(apiQuality)
	if v, ok := computeErrorRateFromProm(ctx); ok {
		nonAdminErrorRate = v
		sources["error_rate"] = "prom"
	}

	alertsOverview := buildAlertsOverview(downCount, messagePipeline, sloOverview)
	downstreamWriteQuality := buildDownstreamWriteQuality(metricFamilies, messagePipeline)

	pushSuccessRate := 0.0
	if gp, ok := downstreamWriteQuality["gateway_push"].(gin.H); ok {
		if v, ok2 := gp["success_rate"].(float64); ok2 {
			pushSuccessRate = v * 100
		}
	}
	if v, ok := computePushSuccessRateFromProm(ctx); ok {
		pushSuccessRate = v
		sources["push_success_rate"] = "prom"
	}

	kafkaRetryLike := 0.0
	if kw, ok := downstreamWriteQuality["kafka_write"].(gin.H); ok {
		if v, ok2 := kw["retry_like_total"].(int64); ok2 {
			kafkaRetryLike = float64(v)
		}
	}
	if v, ok := computeKafkaRetryTotalFromProm(ctx); ok {
		kafkaRetryLike = v
		sources["kafka_retry_total"] = "prom"
	}

	// 写入本地 timeseries 存储，供折线图查询；不管数据是 prom 还是 local，统一沉淀。
	appendMetricPoint("ingress_qps", ingressQPS)
	if v, ok := sloOverview["api_p95_now_ms"].(int64); ok {
		appendMetricPoint("api_p95_ms", float64(v))
	}
	appendMetricPoint("error_rate", nonAdminErrorRate)
	appendMetricPoint("push_success_rate", pushSuccessRate)
	appendMetricPoint("kafka_retry_total", kafkaRetryLike)

	// 诊断用 meta：探针与本请求中已成功使用的 Prom 结果交叉验证，避免「能查数但仍提示未连上」的误报。
	probeProm := promql.Probe(ctx)
	promFromSources := false
	for _, v := range sources {
		if s, ok := v.(string); ok && s == "prom" {
			promFromSources = true
			break
		}
	}
	promForMeta := probeProm || promFromSources
	gwScrapeForMeta := gatewayMetricsScraped
	if !gwScrapeForMeta {
		gwScrapeForMeta = metricFamiliesHasGatewayScrapeContent(metricFamilies)
	}
	promPimGatewayUp := -1.0
	if promForMeta {
		if v, _, ok := promql.Scalar(ctx, `sum(up{job="pim-gateway"})`); ok {
			promPimGatewayUp = v
		}
	}
	// 只要 Prom 可用（探针或本请求任一路已通），与集群 HTTP/聚合 相关的徽标统一标为 prom；数值仍可为 0 或回退（由上面各分支写入）。
	if promForMeta {
		for _, k := range []string{
			"ingress_qps", "api_p95_ms", "error_rate", "push_success_rate", "kafka_retry_total",
			"api_quality", "message_pipeline", "slo_overview", "service_instances", "timeseries",
		} {
			sources[k] = "prom"
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"overview_meta": gin.H{
			"prometheus_reachable":        promForMeta,
			"prometheus_reachable_probe":  probeProm,
			"gateway_metrics_scraped":     gwScrapeForMeta,
			"gateway_scrape_url_reachable": gatewayMetricsScraped,
			"prom_pim_gateway_targets_up":  promPimGatewayUp, // 来自 Prom 的 job=pim-gateway；-1=未取到
		},
		"service_health":    serviceHealth,
		"service_instances": serviceInstances,
		"service_health_summary": gin.H{
			"up":    svcUp,
			"total": svcTotal,
		},
		"api_quality":      apiQuality,
		"message_pipeline": messagePipeline,
		"gateway_connections": gin.H{
			"node":               s.NodeID,
			"active_connections": metricGauge(metricFamilies, "pim_gateway_ws_connections"),
			"connect_rate":       connectRate,
			"disconnect_rate":    disconnectRate,
		},
		"timeseries": gin.H{
			"window":            windowDur.String(),
			"ingress_qps":       metricSeriesByWindow("ingress_qps", windowDur),
			"api_p95_ms":        metricSeriesByWindow("api_p95_ms", windowDur),
			"error_rate":        metricSeriesByWindow("error_rate", windowDur),
			"push_success_rate": metricSeriesByWindow("push_success_rate", windowDur),
			"kafka_retry_total": metricSeriesByWindow("kafka_retry_total", windowDur),
			"api_domain":        metricSeriesByPrefixWindow("api_domain|", windowDur),
			"api_route":         metricSeriesByPrefixWindow("api_route|", windowDur),
		},
		"downstream_write_quality": downstreamWriteQuality,
		"alerts_overview":          alertsOverview,
		"slo_overview":             sloOverview,
		"sources":                  sources,
		"scopes":                   scopes,
		"bench_im_ws":              benchIM,
		"bench_run":                snapshotBenchRun(),
		"bench_runs":               snapshotBenchRuns(),
		"generated_at":             time.Now().Format(time.RFC3339),
	})
}

// overlayAPIQualityFromProm 用 PromQL 覆盖 API 表格里的 qps / error_rate / p95_latency_ms。
// 三个表达式分别查，任何一个成功都会部分 overlay；全部失败返回 false（保持本地快照值）。
// 返回值 ok=true 表示至少有一个字段被 Prom 覆盖，调用方据此把 source 标记成 prom。
func overlayAPIQualityFromProm(ctx context.Context, apiQuality []gin.H) bool {
	qpsMap := map[string]float64{}
	_, _, qpsOK := func() ([]promql.Sample, string, bool) {
		samples, src, ok := promql.InstantVec(ctx, `sum by (route) (rate(pim_http_requests_total{service="gateway"}[1m]))`)
		if ok {
			for _, s := range samples {
				if r := s.Labels["route"]; r != "" {
					qpsMap[r] = s.Value
				}
			}
		}
		return samples, src, ok
	}()

	errMap := map[string]float64{}
	_, _, errOK := func() ([]promql.Sample, string, bool) {
		samples, src, ok := promql.InstantVec(ctx,
			`sum by (route) (rate(pim_http_requests_total{service="gateway",status=~"4..|5.."}[1m])) / sum by (route) (rate(pim_http_requests_total{service="gateway"}[1m]))`)
		if ok {
			for _, s := range samples {
				if r := s.Labels["route"]; r != "" && !math.IsNaN(s.Value) {
					errMap[r] = s.Value
				}
			}
		}
		return samples, src, ok
	}()

	p95Map := map[string]float64{}
	_, _, p95OK := func() ([]promql.Sample, string, bool) {
		samples, src, ok := promql.InstantVec(ctx,
			`histogram_quantile(0.95, sum by (le, route) (rate(pim_http_request_duration_seconds_bucket{service="gateway"}[5m]))) * 1000`)
		if ok {
			for _, s := range samples {
				if r := s.Labels["route"]; r != "" && !math.IsNaN(s.Value) {
					p95Map[r] = s.Value
				}
			}
		}
		return samples, src, ok
	}()

	p99Map := map[string]float64{}
	_, _, p99OK := func() ([]promql.Sample, string, bool) {
		samples, src, ok := promql.InstantVec(ctx,
			`histogram_quantile(0.99, sum by (le, route) (rate(pim_http_request_duration_seconds_bucket{service="gateway"}[5m]))) * 1000`)
		if ok {
			for _, s := range samples {
				if r := s.Labels["route"]; r != "" && !math.IsNaN(s.Value) {
					p99Map[r] = s.Value
				}
			}
		}
		return samples, src, ok
	}()

	if !qpsOK && !errOK && !p95OK && !p99OK {
		return false
	}
	for _, item := range apiQuality {
		route := strings.TrimSpace(fmt.Sprint(item["route"]))
		if route == "" {
			continue
		}
		if qpsOK {
			if v, ok := qpsMap[route]; ok {
				item["qps"] = math.Round(v*1000) / 1000
			}
		}
		if errOK {
			if v, ok := errMap[route]; ok {
				item["error_rate"] = math.Round(v*100000) / 100000
			}
		}
		if p95OK {
			if v, ok := p95Map[route]; ok {
				item["p95_latency_ms"] = int64(math.Round(v))
			}
		}
		if p99OK {
			if v, ok := p99Map[route]; ok {
				item["p99_latency_ms"] = int64(math.Round(v))
			}
		}
	}
	return true
}

// overlayTopicRatesFromProm 用 PromQL 覆盖 message_pipeline 各 topic 的速率与 handler p95。
// 仅在 produce 查询成功时返回 true；consume 与 p95 独立降级，不影响整体判定。
func overlayTopicRatesFromProm(ctx context.Context, messagePipeline []gin.H) bool {
	prodMap := map[string]float64{}
	samples, _, produceOK := promql.InstantVec(ctx, `sum by (topic) (rate(pim_kafka_messages_total{direction="produce",result="ok"}[1m]))`)
	if produceOK {
		for _, s := range samples {
			if t := s.Labels["topic"]; t != "" {
				prodMap[t] = s.Value
			}
		}
	}
	conMap := map[string]float64{}
	if samples, _, ok := promql.InstantVec(ctx, `sum by (topic) (rate(pim_kafka_messages_total{direction="consume",result="ok"}[1m]))`); ok {
		for _, s := range samples {
			if t := s.Labels["topic"]; t != "" {
				conMap[t] = s.Value
			}
		}
	}
	p95Map := map[string]float64{}
	if samples, _, ok := promql.InstantVec(ctx,
		`histogram_quantile(0.95, sum by (le, topic) (rate(pim_kafka_handler_duration_seconds_bucket{result="ok"}[5m]))) * 1000`); ok {
		for _, s := range samples {
			if t := s.Labels["topic"]; t != "" && !math.IsNaN(s.Value) {
				p95Map[t] = s.Value
			}
		}
	}

	if !produceOK {
		return false
	}
	for _, item := range messagePipeline {
		topic, _ := item["topic"].(string)
		if topic == "" {
			continue
		}
		if v, ok := prodMap[topic]; ok {
			item["produce_rate"] = math.Round(v*1000) / 1000
		}
		if v, ok := conMap[topic]; ok {
			item["consume_rate"] = math.Round(v*1000) / 1000
		}
		if v, ok := p95Map[topic]; ok {
			item["e2e_p95_ms"] = int64(math.Round(v))
		}
	}
	return true
}

func computeIngressQPSFromProm(ctx context.Context) (float64, bool) {
	v, _, ok := promql.Scalar(ctx, `sum(rate(pim_http_requests_total{service="gateway",route!~"/api/v1/admin/.*"}[1m]))`)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return math.Round(v*1000) / 1000, true
}

func computeAPIP95MSFromProm(ctx context.Context) (int64, bool) {
	// 运行概览顶栏「API p95」用 [1m]：对「压测刚停」后仍用 [5m] 时易把 5 分钟内尾延迟与空窗混进分位数，p95 常顶在 5s 桶（显示约 5000ms）；1m 更贴「当前」HTTP。
	// /ws 在网关侧由 handleWS 阻塞到 WebSocket 关闭，HTTP 直方图记的是「整段连接时长」，会远高于 REST；顶栏「API p95」排除 /ws 以免与 e2e/ack 消息延迟混淆。
	v, _, ok := promql.Scalar(ctx,
		`histogram_quantile(0.95, sum by (le) (rate(pim_http_request_duration_seconds_bucket{service="gateway",route!~"/api/v1/admin/.*",route!="/ws"}[1m]))) * 1000`)
	if !ok || math.IsNaN(v) {
		return 0, false
	}
	if math.IsInf(v, 0) {
		return 0, false
	}
	return int64(math.Round(v)), true
}

func computeErrorRateFromProm(ctx context.Context) (float64, bool) {
	v, _, ok := promql.Scalar(ctx,
		`sum(rate(pim_http_requests_total{service="gateway",status=~"4..|5..",route!~"/api/v1/admin/.*"}[1m])) / sum(rate(pim_http_requests_total{service="gateway",route!~"/api/v1/admin/.*"}[1m]))`)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return math.Round(v*100000) / 100000, true
}

// computePushSuccessRateFromProm 返回集群 push 成功率（0~100 的百分比，与本地口径一致）。
func computePushSuccessRateFromProm(ctx context.Context) (float64, bool) {
	v, _, ok := promql.Scalar(ctx,
		`(sum(rate(pim_gateway_push_total{result="ok"}[1m])) / sum(rate(pim_gateway_push_total[1m]))) * 100`)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return math.Round(v*100) / 100, true
}

func computeKafkaRetryTotalFromProm(ctx context.Context) (float64, bool) {
	v, _, ok := promql.Scalar(ctx, `sum(pim_kafka_messages_total{result="handler_error"})`)
	if !ok || math.IsNaN(v) {
		return 0, false
	}
	return v, true
}

// computeMsgE2EP95MSFromProm 返回服务端侧 e2e p95（毫秒）。
// 对应 bench.e2e 的服务端段：sender gateway WS 入口 → receiver gateway 推送出口。
// 直方图 pim_im_e2e_server_seconds 只在 im_push_consumer 推送成功后 Observe。
func computeMsgE2EP95MSFromProm(ctx context.Context) (int64, bool) {
	v, _, ok := promql.Scalar(ctx,
		`histogram_quantile(0.95, sum by (le) (rate(pim_im_e2e_server_seconds_bucket[5m]))) * 1000`)
	if !ok || math.IsNaN(v) || v <= 0 {
		return 0, false
	}
	return int64(math.Round(v)), true
}

// computeWSAckP95MSFromProm 返回 gateway WS 收帧 → 回 ack 的 p95（毫秒）。
// 对应 bench.ack 的服务端段（不含客户端网络 RTT）。
func computeWSAckP95MSFromProm(ctx context.Context) (int64, bool) {
	v, _, ok := promql.Scalar(ctx,
		`histogram_quantile(0.95, sum by (le) (rate(pim_gateway_ws_send_duration_seconds_bucket[5m]))) * 1000`)
	if !ok || math.IsNaN(v) || v <= 0 {
		return 0, false
	}
	return int64(math.Round(v)), true
}

func computeWSAckP99MSFromProm(ctx context.Context) (int64, bool) {
	v, _, ok := promql.Scalar(ctx,
		`histogram_quantile(0.99, sum by (le) (rate(pim_gateway_ws_send_duration_seconds_bucket[5m]))) * 1000`)
	if !ok || math.IsNaN(v) || v <= 0 {
		return 0, false
	}
	return int64(math.Round(v)), true
}

func computeMsgE2EP99MSFromProm(ctx context.Context) (int64, bool) {
	v, _, ok := promql.Scalar(ctx,
		`histogram_quantile(0.99, sum by (le) (rate(pim_im_e2e_server_seconds_bucket[5m]))) * 1000`)
	if !ok || math.IsNaN(v) || v <= 0 {
		return 0, false
	}
	return int64(math.Round(v)), true
}

// collectServiceInstancesFromProm 用 PromQL up{job=~"pim-.*"} 枚举所有 service 实例的 up 状态，
// 让前端能看到「哪一个实例挂了」，而不是只看到「本网关能否联上依赖」。
// 返回 instances 列表和 [upCount, totalCount] 汇总；Prom 不可用时返回 ok=false。
func collectServiceInstancesFromProm(ctx context.Context) ([]gin.H, int, int, bool) {
	samples, _, ok := promql.InstantVec(ctx, `up{job=~"pim-.*"}`)
	if !ok {
		return nil, 0, 0, false
	}
	out := make([]gin.H, 0, len(samples))
	upCount := 0
	for _, s := range samples {
		job := s.Labels["job"]
		inst := s.Labels["instance"]
		service := s.Labels["service"]
		if service == "" {
			service = strings.TrimPrefix(job, "pim-")
		}
		isUp := s.Value >= 0.5
		if isUp {
			upCount++
		}
		out = append(out, gin.H{
			"job":      job,
			"instance": inst,
			"service":  service,
			"up":       isUp,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		si, _ := out[i]["service"].(string)
		sj, _ := out[j]["service"].(string)
		if si != sj {
			return si < sj
		}
		ii, _ := out[i]["instance"].(string)
		ij, _ := out[j]["instance"].(string)
		return ii < ij
	})
	return out, upCount, len(out), true
}

func computeIngressQPS(apiQuality []gin.H) float64 {
	total := 0.0
	for _, item := range apiQuality {
		route, _ := item["route"].(string)
		if strings.HasPrefix(route, "/api/v1/admin/") {
			continue
		}
		if v, ok := item["qps"].(float64); ok {
			total += v
		}
	}
	return math.Round(total*1000) / 1000
}

func computeNonAdminErrorRate(apiQuality []gin.H) float64 {
	sum := 0.0
	count := 0.0
	for _, item := range apiQuality {
		route, _ := item["route"].(string)
		if strings.HasPrefix(route, "/api/v1/admin/") {
			continue
		}
		if v, ok := item["error_rate"].(float64); ok {
			sum += v
			count++
		}
	}
	if count <= 0 {
		return 0
	}
	return math.Round((sum/count)*100000) / 100000
}

func parseOverviewWindow(v string) time.Duration {
	switch strings.TrimSpace(v) {
	case "5m":
		return 5 * time.Minute
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	default:
		return 15 * time.Minute
	}
}

func appendMetricPoint(name string, val float64) {
	now := time.Now()
	seriesMu.Lock()
	defer seriesMu.Unlock()
	list := metricSeriesStore[name]
	list = append(list, metricPoint{TS: now, Val: val})
	// Keep roughly latest 24h with 5-10s polling headroom.
	if len(list) > 20000 {
		list = list[len(list)-20000:]
	}
	metricSeriesStore[name] = list
}

func metricSeriesByWindow(name string, d time.Duration) []gin.H {
	seriesMu.Lock()
	list := append([]metricPoint(nil), metricSeriesStore[name]...)
	seriesMu.Unlock()
	if len(list) == 0 {
		return []gin.H{}
	}
	cutoff := time.Now().Add(-d)
	out := make([]gin.H, 0, len(list))
	for _, p := range list {
		if p.TS.Before(cutoff) {
			continue
		}
		out = append(out, gin.H{
			"ts":  p.TS.Format(time.RFC3339),
			"val": math.Round(p.Val*1000) / 1000,
		})
	}
	return out
}

func metricSeriesByPrefixWindow(prefix string, d time.Duration) gin.H {
	seriesMu.Lock()
	storeCopy := map[string][]metricPoint{}
	for k, v := range metricSeriesStore {
		if strings.HasPrefix(k, prefix) {
			storeCopy[k] = append([]metricPoint(nil), v...)
		}
	}
	seriesMu.Unlock()
	cutoff := time.Now().Add(-d)
	out := gin.H{}
	for key, list := range storeCopy {
		rest := strings.TrimPrefix(key, prefix)
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) != 2 {
			continue
		}
		entity := parts[0]
		metric := parts[1]
		item, ok := out[entity].(gin.H)
		if !ok {
			item = gin.H{}
			out[entity] = item
		}
		pts := make([]gin.H, 0, len(list))
		for _, p := range list {
			if p.TS.Before(cutoff) {
				continue
			}
			pts = append(pts, gin.H{
				"ts":  p.TS.Format(time.RFC3339),
				"val": math.Round(p.Val*1000) / 1000,
			})
		}
		item[metric] = pts
	}
	return out
}

func appendAPITimeseries(apiQuality []gin.H) {
	type agg struct {
		qpsSum   float64
		errSum   float64
		errCount float64
		p95Sum   float64
		p95Count float64
	}
	byDomain := map[string]*agg{}
	for _, item := range apiQuality {
		route := strings.TrimSpace(fmt.Sprint(item["route"]))
		if route == "" {
			continue
		}
		qps := toFloat64(item["qps"])
		errRate := toFloat64(item["error_rate"])
		p95 := toFloat64(item["p95_latency_ms"])

		appendMetricPoint(fmt.Sprintf("api_route|%s|qps", route), qps)
		appendMetricPoint(fmt.Sprintf("api_route|%s|error_rate", route), errRate)
		appendMetricPoint(fmt.Sprintf("api_route|%s|p95_ms", route), p95)

		domain := routeDomain(route)
		a := byDomain[domain]
		if a == nil {
			a = &agg{}
			byDomain[domain] = a
		}
		a.qpsSum += qps
		a.errSum += errRate
		a.errCount++
		if p95 > 0 {
			a.p95Sum += p95
			a.p95Count++
		}
	}
	for domain, a := range byDomain {
		avgErr := 0.0
		if a.errCount > 0 {
			avgErr = a.errSum / a.errCount
		}
		avgP95 := 0.0
		if a.p95Count > 0 {
			avgP95 = a.p95Sum / a.p95Count
		}
		appendMetricPoint(fmt.Sprintf("api_domain|%s|qps", domain), a.qpsSum)
		appendMetricPoint(fmt.Sprintf("api_domain|%s|error_rate", domain), avgErr)
		appendMetricPoint(fmt.Sprintf("api_domain|%s|p95_ms", domain), avgP95)
	}
}

func applyAPIQualityWindowAggregates(apiQuality []gin.H, d time.Duration) {
	for _, item := range apiQuality {
		route := strings.TrimSpace(fmt.Sprint(item["route"]))
		if route == "" {
			continue
		}
		if avg, ok := metricWindowAverageByKey(fmt.Sprintf("api_route|%s|qps", route), d); ok {
			item["qps"] = math.Round(avg*1000) / 1000
		}
		if avg, ok := metricWindowAverageByKey(fmt.Sprintf("api_route|%s|error_rate", route), d); ok {
			item["error_rate"] = math.Round(avg*100000) / 100000
		}
		if avg, ok := metricWindowAverageByKey(fmt.Sprintf("api_route|%s|p95_ms", route), d); ok {
			item["p95_latency_ms"] = int64(math.Round(avg))
		}
	}
}

func metricWindowAverageByKey(name string, d time.Duration) (float64, bool) {
	seriesMu.Lock()
	list := append([]metricPoint(nil), metricSeriesStore[name]...)
	seriesMu.Unlock()
	if len(list) == 0 {
		return 0, false
	}
	cutoff := time.Now().Add(-d)
	sum := 0.0
	count := 0.0
	for _, p := range list {
		if p.TS.Before(cutoff) {
			continue
		}
		sum += p.Val
		count++
	}
	if count <= 0 {
		return 0, false
	}
	return sum / count, true
}

func toFloat64(v interface{}) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint64:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
	}
}

func (s *Service) collectServiceHealth(ctx context.Context) ([]gin.H, int) {
	type svc struct {
		ID  string
		URL string
	}
	targets := []svc{
		{ID: "gateway", URL: "self"},
		{ID: "auth", URL: config.AdminHealthAuthURL},
		{ID: "user", URL: config.AdminHealthUserURL},
		{ID: "friend", URL: config.AdminHealthFriendURL},
		{ID: "conversation", URL: config.AdminHealthConversationURL},
		{ID: "group", URL: config.AdminHealthGroupURL},
		{ID: "file", URL: s.FileServiceBaseURL + "/health"},
		{ID: "log", URL: s.LogServiceBaseURL + "/health"},
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

// gatherMetricFamilies 优先从 Gateway 的 /metrics 拉文本解析；失败则回退为 observe 本进程 Gatherer。
// 第二个返回值为 true 表示已成功从 Gateway 拉到非空指标族（与迁出前「落在网关内读自身」对齐）。
func (s *Service) gatherMetricFamilies() (map[string]*dto.MetricFamily, bool) {
	scrape := strings.TrimSpace(s.GatewayMetricsScrapeURL)
	if scrape != "" {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, scrape, nil)
		if err == nil {
			resp, err := http.DefaultClient.Do(req)
			if err == nil && resp != nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					// prometheus/common 新 API：零值 TextParser 的 scheme 为 unset 会 panic，须显式 UTF8 校验方案。
					p := expfmt.NewTextParser(model.UTF8Validation)
					m, err := p.TextToMetricFamilies(resp.Body)
					if err == nil && len(m) > 0 {
						return m, true
					}
				}
			}
		}
	}
	out := map[string]*dto.MetricFamily{}
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return out, false
	}
	for _, mf := range mfs {
		if mf == nil || mf.Name == nil {
			continue
		}
		out[mf.GetName()] = mf
	}
	return out, false
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
			"qps":            0, // filled by fillRateSnapshots.
			"error_rate":     errorRate,
			"p95_latency_ms": estimateHTTPRouteP95MS(mfs, k.route),
			"p99_latency_ms": 0,
		})
	}
	return out
}

func mergeAPIQualityWithRouteCatalog(observed []gin.H, catalog []string) []gin.H {
	if len(catalog) == 0 {
		return observed
	}
	byRoute := make(map[string]gin.H, len(observed))
	for _, it := range observed {
		route := strings.TrimSpace(fmt.Sprint(it["route"]))
		if route == "" {
			continue
		}
		byRoute[route] = it
	}
	merged := make([]gin.H, 0, len(catalog))
	for _, route := range catalog {
		if it, ok := byRoute[route]; ok {
			merged = append(merged, it)
			continue
		}
		merged = append(merged, gin.H{
			"service":        "gateway",
			"route":          route,
			"qps":            0,
			"error_rate":     0,
			"p95_latency_ms": 0,
			"p99_latency_ms": 0,
		})
	}
	return merged
}

func routeDomain(route string) string {
	switch {
	case strings.HasPrefix(route, "/api/v1/friends"):
		return "friend-service"
	case strings.HasPrefix(route, "/api/v1/groups"):
		return "group-service"
	case strings.HasPrefix(route, "/api/v1/conversations"), strings.HasPrefix(route, "/api/v1/messages"):
		return "conversation-service"
	case strings.HasPrefix(route, "/api/v1/files"):
		return "file-service"
	case strings.HasPrefix(route, "/api/v1/logs"):
		return "log-service"
	case strings.HasPrefix(route, "/api/v1/admin"):
		return "gateway-admin"
	case route == "/api/v1/login" || route == "/api/v1/register" || route == "/api/v1/me":
		return "auth-user"
	default:
		return "gateway-other"
	}
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
		k := apiSnapshotKey(service, route)
		cur, okCur := current.apiTotals[k]
		prevVal, okPrev := prev.apiTotals[k]
		// Some metric streams may miss "service" label. Fallback to route-level key.
		if !okCur {
			cur = current.apiTotals[apiSnapshotKey("", route)]
		}
		if !okPrev {
			prevVal = prev.apiTotals[apiSnapshotKey("", route)]
		}
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
		if route == "" {
			continue
		}
		k := apiSnapshotKey(service, route)
		out[k] += metric.GetCounter().GetValue()
	}
	return out
}

func apiSnapshotKey(service, route string) string {
	route = strings.TrimSpace(route)
	service = strings.TrimSpace(service)
	if service == "" {
		return "|" + route
	}
	return service + "|" + route
}

func estimateHTTPRouteP95MS(mfs map[string]*dto.MetricFamily, route string) int64 {
	route = strings.TrimSpace(route)
	if route == "" {
		return 0
	}
	mf := mfs["pim_http_request_duration_seconds"]
	if mf == nil {
		return 0
	}
	byLE := map[float64]float64{}
	for _, metric := range mf.Metric {
		if labelValue(metric, "route") != route {
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
			denom := cur - prevCount
			if denom <= 0 {
				return int64(math.Round(le * 1000))
			}
			ratio := (target - prevCount) / denom
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
		if route == "/ws" {
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
			denom := cur - prevCount
			if denom <= 0 {
				return int64(math.Round(le * 1000))
			}
			ratio := (target - prevCount) / denom
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
			denom := cur - prevCount
			if denom <= 0 {
				return int64(math.Round(le * 1000))
			}
			ratio := (target - prevCount) / denom
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

func buildDownstreamWriteQuality(mfs map[string]*dto.MetricFamily, messagePipeline []gin.H) gin.H {
	produceTotal := int64(0)
	consumeTotal := int64(0)
	retryLikeTotal := int64(0)
	dlqTotal := int64(0)
	maxHandlerP95 := int64(0)
	for _, item := range messagePipeline {
		if v, ok := item["produce_total"].(int64); ok {
			produceTotal += v
		}
		if v, ok := item["consume_total"].(int64); ok {
			consumeTotal += v
		}
		if v, ok := item["retry_count"].(int64); ok {
			retryLikeTotal += v
		}
		if v, ok := item["dlq_count"].(int64); ok {
			dlqTotal += v
		}
		if v, ok := item["e2e_p95_ms"].(int64); ok && v > maxHandlerP95 {
			maxHandlerP95 = v
		}
	}

	pushOk := int64(metricCounterByLabel(mfs, "pim_gateway_push_total", "result", "ok"))
	pushFail := int64(metricCounterExcludeLabelValue(mfs, "pim_gateway_push_total", "result", "ok"))
	pushRate := 1.0
	if pushOk+pushFail > 0 {
		pushRate = float64(pushOk) / float64(pushOk+pushFail)
	}

	return gin.H{
		"gateway_ingress": gin.H{
			"dispatch_p95_ms":      estimateHistogramP95MSByLabels(mfs, "pim_group_ingress_stage_duration_seconds", map[string]string{"stage": "dispatch", "result": "ok"}),
			"member_check_p95_ms":  estimateHistogramP95MSByLabels(mfs, "pim_group_member_check_stage_duration_seconds", map[string]string{"stage": "total", "result": "ok"}),
			"ingress_total_p95_ms": estimateHistogramP95MSByLabels(mfs, "pim_group_ingress_stage_duration_seconds", map[string]string{"stage": "total", "result": "ok"}),
		},
		"kafka_write": gin.H{
			"produce_total":   produceTotal,
			"consume_total":   consumeTotal,
			"retry_like_total": retryLikeTotal,
			"dlq_total":       dlqTotal,
			"handler_p95_ms":  maxHandlerP95,
		},
		"gateway_push": gin.H{
			"ok_total":      pushOk,
			"fail_total":    pushFail,
			"success_rate":  math.Round(pushRate*10000) / 10000,
			"delivery_p95_ms": estimateHistogramP95MSByLabels(mfs, "pim_kafka_handler_duration_seconds", map[string]string{"topic": "im-message-push", "result": "ok"}),
		},
	}
}

func metricCounterByLabel(mfs map[string]*dto.MetricFamily, metricName, label, value string) float64 {
	mf := mfs[metricName]
	if mf == nil {
		return 0
	}
	total := 0.0
	for _, m := range mf.Metric {
		if labelValue(m, label) == value {
			total += m.GetCounter().GetValue()
		}
	}
	return total
}

func metricCounterExcludeLabelValue(mfs map[string]*dto.MetricFamily, metricName, label, excluded string) float64 {
	mf := mfs[metricName]
	if mf == nil {
		return 0
	}
	total := 0.0
	for _, m := range mf.Metric {
		if labelValue(m, label) == excluded {
			continue
		}
		total += m.GetCounter().GetValue()
	}
	return total
}

func estimateHistogramP95MSByLabels(mfs map[string]*dto.MetricFamily, metricName string, labels map[string]string) int64 {
	mf := mfs[metricName]
	if mf == nil {
		return 0
	}
	byLE := map[float64]float64{}
	for _, metric := range mf.Metric {
		matched := true
		for k, want := range labels {
			if labelValue(metric, k) != want {
				matched = false
				break
			}
		}
		if !matched {
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
			denom := cur - prevCount
			if denom <= 0 {
				return int64(math.Round(le * 1000))
			}
			ratio := (target - prevCount) / denom
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

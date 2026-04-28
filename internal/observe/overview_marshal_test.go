package observe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// TestHandleAdminObservabilityOverviewJSON 保证概览 JSON 可序列化（排 NaN/Inf/非法结构致 500）。
func TestHandleAdminObservabilityOverviewJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Service{
		LogServiceBaseURL:         "http://127.0.0.1:9999",
		FileServiceBaseURL:        "http://127.0.0.1:9998",
		NodeID:                    "t",
		Redis:                     nil,
		APIRouteCatalog:           nil,
		GatewayMetricsScrapeURL:   "http://127.0.0.1:26080/metrics",
	}
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterRoutes(s, r)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/observability/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	_, hasBench := m["bench_run"]
	if !hasBench {
		t.Fatal("expected bench_run key")
	}
	_, hasBenchRuns := m["bench_runs"]
	if !hasBenchRuns {
		t.Fatal("expected bench_runs key")
	}
}

func TestHandleAdminBenchRunAndOverview(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Service{
		LogServiceBaseURL:       "http://127.0.0.1:9999",
		FileServiceBaseURL:      "http://127.0.0.1:9998",
		NodeID:                  "t",
		Redis:                   nil,
		APIRouteCatalog:         nil,
		GatewayMetricsScrapeURL: "http://127.0.0.1:26080/metrics",
	}
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterRoutes(s, r)
	body := `{"schema":"pim_bench_msg_latency_v1","finished_at":"2026-04-25T00:00:00Z","measure":"e2e","n":4,"e2e_ms":{"avg":1,"p95":2,"p99":3}}`
	preq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/observability/bench-run", strings.NewReader(body))
	preq.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	r.ServeHTTP(pw, preq)
	if pw.Code != http.StatusOK {
		t.Fatalf("post status=%d %s", pw.Code, pw.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/observability/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d", w.Code)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	br, _ := m["bench_run"].(map[string]interface{})
	if br == nil {
		t.Fatal("expected non-nil bench_run after post")
	}
	if br["measure"] != "e2e" {
		t.Fatalf("measure: %v", br["measure"])
	}
	brs, _ := m["bench_runs"].(map[string]interface{})
	if brs == nil {
		t.Fatal("expected bench_runs map after post")
	}
	if brs["msg-latency:e2e"] == nil {
		t.Fatal("expected bench_runs[\"msg-latency:e2e\"] after e2e post")
	}
	if brs["msg-latency:ack"] != nil {
		t.Fatal("expected no ack slot before ack post")
	}
	bodyAck := `{"schema":"pim_bench_msg_latency_v1","finished_at":"2026-04-25T00:00:01Z","measure":"ack","n":4,"ack_ms":{"avg":1,"p95":2,"p99":3}}`
	preq2 := httptest.NewRequest(http.MethodPost, "/api/v1/admin/observability/bench-run", strings.NewReader(bodyAck))
	preq2.Header.Set("Content-Type", "application/json")
	pw2 := httptest.NewRecorder()
	r.ServeHTTP(pw2, preq2)
	if pw2.Code != http.StatusOK {
		t.Fatalf("post ack status=%d %s", pw2.Code, pw2.Body.String())
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/observability/overview", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	var m2 map[string]interface{}
	_ = json.Unmarshal(w2.Body.Bytes(), &m2)
	brs2, _ := m2["bench_runs"].(map[string]interface{})
	if brs2 == nil || brs2["msg-latency:e2e"] == nil || brs2["msg-latency:ack"] == nil {
		t.Fatalf("expected both msg-latency e2e and ack in bench_runs: %#v", brs2)
	}
	br2, _ := m2["bench_run"].(map[string]interface{})
	if br2 == nil || br2["measure"] != "ack" {
		t.Fatalf("bench_run should be last post ack: %#v", br2)
	}
}

func TestHandleAdminBenchRunGroupBroadcastSlot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Service{
		LogServiceBaseURL:       "http://127.0.0.1:9999",
		FileServiceBaseURL:      "http://127.0.0.1:9998",
		NodeID:                  "t",
		Redis:                   nil,
		APIRouteCatalog:         nil,
		GatewayMetricsScrapeURL: "http://127.0.0.1:26080/metrics",
	}
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterRoutes(s, r)
	body := `{"schema":"pim_bench_group_broadcast_v1","finished_at":"2026-04-28T12:00:00Z","measure":"e2e","bench_name":"gb","n":100,"e2e_ms":{"avg":1,"p95":2,"p99":3}}`
	preq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/observability/bench-run", strings.NewReader(body))
	preq.Header.Set("Content-Type", "application/json")
	pw := httptest.NewRecorder()
	r.ServeHTTP(pw, preq)
	if pw.Code != http.StatusOK {
		t.Fatalf("post status=%d %s", pw.Code, pw.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/observability/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var m map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	brs, _ := m["bench_runs"].(map[string]interface{})
	if brs == nil || brs["group-broadcast:e2e"] == nil {
		t.Fatalf("expected group-broadcast:e2e in bench_runs: %#v", brs)
	}
}

// TestHandleWithStubContext 可选手动连本机服务时取消 skip。
func TestHandleAdminObservabilityOverviewLive(t *testing.T) {
	if testing.Short() {
		t.Skip("live")
	}
	gin.SetMode(gin.TestMode)
	s := &Service{
		LogServiceBaseURL:         "http://127.0.0.1:26016",
		FileServiceBaseURL:        "http://127.0.0.1:26006",
		NodeID:                    "t",
		Redis:                     redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}),
		APIRouteCatalog:           nil,
		GatewayMetricsScrapeURL:   "http://127.0.0.1:26080/metrics",
	}
	_ = s.Redis.Ping(context.Background()).Err()
	r := gin.New()
	r.Use(gin.Recovery())
	RegisterRoutes(s, r)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/observability/overview", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	t.Logf("status=%d len=%d", w.Code, w.Body.Len())
	if w.Code != http.StatusOK {
		t.Fatalf("body=%q", w.Body.String())
	}
}

package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"pim/internal/config"
)

// PromSample 是 instant-vector 查询（/api/v1/query）里一条结果：一组标签 + 一个数值。
type PromSample struct {
	Labels map[string]string
	Value  float64
}

// PromPoint 是 range-matrix 查询（/api/v1/query_range）里一个时间点。
type PromPoint struct {
	TS  time.Time
	Val float64
}

// PromSeries 是 range-matrix 里一条时间序列：一组标签 + 多个点。
type PromSeries struct {
	Labels map[string]string
	Points []PromPoint
}

type promVectorResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type promMatrixResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// promCacheEntry 用固定 TTL，避免短时间内多次刷新时同表达式反复走网络。
type promCacheEntry struct {
	expireAt time.Time
	vec      []PromSample
	mat      []PromSeries
}

var (
	promCacheMu  sync.Mutex
	promCacheVec = map[string]promCacheEntry{}
	promCacheMat = map[string]promCacheEntry{}
)

// promCacheTTL 刻意取 1.5s：Prom 抓取间隔 15s，后台页面通常 5s 刷新；
// 即使有多个 admin 同时查询，也能被 TTL 合并为每 ~1.5s 最多一次实打实的 Prom 请求。
const promCacheTTL = 1500 * time.Millisecond

// promHTTPTimeout 单次 PromQL 查询的超时上限，超过则视为不可用、走本地降级。
const promHTTPTimeout = 1500 * time.Millisecond

// promBaseURL 返回去掉尾部斜杠的 Prom URL；为空表示禁用。
func promBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(config.PrometheusQueryURL), "/")
}

// promInstantVec 执行 PromQL instant 查询，返回 []PromSample、数据源标签与是否成功。
// 约定：
//   - 返回 source="prom"  表示数据来自 Prometheus
//   - 返回 source="local" 表示 Prom 不可用，调用方应降级
func promInstantVec(ctx context.Context, expr string) ([]PromSample, string, bool) {
	base := promBaseURL()
	if base == "" {
		return nil, "local", false
	}
	key := "v|" + expr
	if cached, ok := lookupPromCacheVec(key); ok {
		return cached, "prom", true
	}

	q := url.Values{}
	q.Set("query", expr)
	u := base + "/api/v1/query?" + q.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, promHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "local", false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "local", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "local", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "local", false
	}
	var pr promVectorResponse
	if err := json.Unmarshal(body, &pr); err != nil || pr.Status != "success" {
		return nil, "local", false
	}

	out := make([]PromSample, 0, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		if len(r.Value) < 2 {
			continue
		}
		s, ok := r.Value[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		out = append(out, PromSample{Labels: r.Metric, Value: v})
	}
	storePromCacheVec(key, out)
	return out, "prom", true
}

// promRangeMatrix 执行 PromQL range 查询（[end-dur, end] 区间），用于折线图。
func promRangeMatrix(ctx context.Context, expr string, step, dur time.Duration) ([]PromSeries, string, bool) {
	base := promBaseURL()
	if base == "" {
		return nil, "local", false
	}
	end := time.Now()
	start := end.Add(-dur)
	stepSec := int(step.Seconds())
	if stepSec <= 0 {
		stepSec = 15
	}
	key := "m|" + strconv.Itoa(stepSec) + "|" + strconv.FormatInt(int64(dur.Seconds()), 10) + "|" + expr
	if cached, ok := lookupPromCacheMat(key); ok {
		return cached, "prom", true
	}

	q := url.Values{}
	q.Set("query", expr)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.Itoa(stepSec)+"s")
	u := base + "/api/v1/query_range?" + q.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, promHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "local", false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "local", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "local", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "local", false
	}
	var pr promMatrixResponse
	if err := json.Unmarshal(body, &pr); err != nil || pr.Status != "success" {
		return nil, "local", false
	}

	out := make([]PromSeries, 0, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		pts := make([]PromPoint, 0, len(r.Values))
		for _, pair := range r.Values {
			if len(pair) < 2 {
				continue
			}
			tsNum, ok := pair[0].(float64)
			if !ok {
				continue
			}
			vs, ok := pair[1].(string)
			if !ok {
				continue
			}
			v, err := strconv.ParseFloat(vs, 64)
			if err != nil {
				continue
			}
			pts = append(pts, PromPoint{
				TS:  time.Unix(int64(tsNum), 0),
				Val: v,
			})
		}
		out = append(out, PromSeries{Labels: r.Metric, Points: pts})
	}
	storePromCacheMat(key, out)
	return out, "prom", true
}

// promScalar 对单点 KPI 的便捷包装：返回 instant 向量第一条值；空值时返回 0 与 ok=false。
func promScalar(ctx context.Context, expr string) (float64, string, bool) {
	samples, src, ok := promInstantVec(ctx, expr)
	if !ok || len(samples) == 0 {
		return 0, src, false
	}
	return samples[0].Value, src, true
}

func lookupPromCacheVec(key string) ([]PromSample, bool) {
	promCacheMu.Lock()
	defer promCacheMu.Unlock()
	entry, ok := promCacheVec[key]
	if !ok || time.Now().After(entry.expireAt) {
		return nil, false
	}
	return entry.vec, true
}

func storePromCacheVec(key string, v []PromSample) {
	promCacheMu.Lock()
	defer promCacheMu.Unlock()
	promCacheVec[key] = promCacheEntry{
		expireAt: time.Now().Add(promCacheTTL),
		vec:      v,
	}
}

func lookupPromCacheMat(key string) ([]PromSeries, bool) {
	promCacheMu.Lock()
	defer promCacheMu.Unlock()
	entry, ok := promCacheMat[key]
	if !ok || time.Now().After(entry.expireAt) {
		return nil, false
	}
	return entry.mat, true
}

func storePromCacheMat(key string, v []PromSeries) {
	promCacheMu.Lock()
	defer promCacheMu.Unlock()
	promCacheMat[key] = promCacheEntry{
		expireAt: time.Now().Add(promCacheTTL),
		mat:      v,
	}
}

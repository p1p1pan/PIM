// Package promql 封装对 Prometheus /api/v1/query 的访问，供 gateway 与 observe 复用。
package promql

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"pim/internal/config"
)

// parsePromFloat64 解析 /api/v1/query 的 value 项：部分 Prom/JSON 组合为字符串，部分为 number。
func parsePromFloat64(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case string:
		f, err := strconv.ParseFloat(x, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// Sample 是 instant-vector 查询里一条结果：一组标签 + 一个数值。
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Point 是 range-matrix 里一个时间点。
type Point struct {
	TS  time.Time
	Val float64
}

// Series 是 range-matrix 里一条时间序列：一组标签 + 多个点。
type Series struct {
	Labels map[string]string
	Points []Point
}

type vectorResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type matrixResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type cacheEntry struct {
	expireAt time.Time
	vec      []Sample
	mat      []Series
}

var (
	cacheMu  sync.Mutex
	cacheVec = map[string]cacheEntry{}
	cacheMat = map[string]cacheEntry{}
)

// CacheTTL 与 gateway 原行为一致，避免短时间重复打 Prom。
const CacheTTL = 1500 * time.Millisecond

// HTTPTimeout 单次查询超时。
const HTTPTimeout = 1500 * time.Millisecond

func baseURL() string {
	return strings.TrimRight(strings.TrimSpace(config.PrometheusQueryURL), "/")
}

// InstantVec 执行 PromQL instant 查询。
func InstantVec(ctx context.Context, expr string) ([]Sample, string, bool) {
	b := baseURL()
	if b == "" {
		return nil, "local", false
	}
	key := "v|" + expr
	if cached, ok := lookupVec(key); ok {
		return cached, "prom", true
	}
	q := url.Values{}
	q.Set("query", expr)
	u := b + "/api/v1/query?" + q.Encode()
	reqCtx, cancel := context.WithTimeout(ctx, HTTPTimeout)
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
	var pr vectorResponse
	if err := json.Unmarshal(body, &pr); err != nil || pr.Status != "success" {
		return nil, "local", false
	}
	out := make([]Sample, 0, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		if len(r.Value) < 2 {
			continue
		}
		v, ok := parsePromFloat64(r.Value[1])
		if !ok || !isFinite(v) {
			continue
		}
		out = append(out, Sample{Labels: r.Metric, Value: v})
	}
	storeVec(key, out)
	return out, "prom", true
}

// RangeMatrix 执行 /api/v1/query_range。
func RangeMatrix(ctx context.Context, expr string, step, dur time.Duration) ([]Series, string, bool) {
	b := baseURL()
	if b == "" {
		return nil, "local", false
	}
	end := time.Now()
	start := end.Add(-dur)
	stepSec := int(step.Seconds())
	if stepSec <= 0 {
		stepSec = 15
	}
	key := "m|" + strconv.Itoa(stepSec) + "|" + strconv.FormatInt(int64(dur.Seconds()), 10) + "|" + expr
	if cached, ok := lookupMat(key); ok {
		return cached, "prom", true
	}
	q := url.Values{}
	q.Set("query", expr)
	q.Set("start", strconv.FormatInt(start.Unix(), 10))
	q.Set("end", strconv.FormatInt(end.Unix(), 10))
	q.Set("step", strconv.Itoa(stepSec)+"s")
	u := b + "/api/v1/query_range?" + q.Encode()
	reqCtx, cancel := context.WithTimeout(ctx, HTTPTimeout)
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
	var pr matrixResponse
	if err := json.Unmarshal(body, &pr); err != nil || pr.Status != "success" {
		return nil, "local", false
	}
	out := make([]Series, 0, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		pts := make([]Point, 0, len(r.Values))
		for _, pair := range r.Values {
			if len(pair) < 2 {
				continue
			}
			tsNum, ok := pair[0].(float64)
			if !ok {
				continue
			}
			v, ok := parsePromFloat64(pair[1])
			if !ok || !isFinite(v) {
				continue
			}
			pts = append(pts, Point{
				TS:  time.Unix(int64(tsNum), 0),
				Val: v,
			})
		}
		out = append(out, Series{Labels: r.Metric, Points: pts})
	}
	storeMat(key, out)
	return out, "prom", true
}

// Probe 对 Prometheus 发一条轻量查询，用于 observe 概览判断 Prom 是否可达（配置、网络）。
func Probe(ctx context.Context) bool {
	_, _, ok := Scalar(ctx, "vector(1)")
	return ok
}

// Scalar 对 instant 向量取第一条标量值。
func Scalar(ctx context.Context, expr string) (float64, string, bool) {
	samples, src, ok := InstantVec(ctx, expr)
	if !ok || len(samples) == 0 {
		return 0, src, false
	}
	v := samples[0].Value
	if !isFinite(v) {
		return 0, src, false
	}
	return v, src, true
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func lookupVec(key string) ([]Sample, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	entry, ok := cacheVec[key]
	if !ok || time.Now().After(entry.expireAt) {
		return nil, false
	}
	return entry.vec, true
}

func storeVec(key string, v []Sample) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheVec[key] = cacheEntry{expireAt: time.Now().Add(CacheTTL), vec: v}
}

func lookupMat(key string) ([]Series, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	entry, ok := cacheMat[key]
	if !ok || time.Now().After(entry.expireAt) {
		return nil, false
	}
	return entry.mat, true
}

func storeMat(key string, v []Series) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cacheMat[key] = cacheEntry{expireAt: time.Now().Add(CacheTTL), mat: v}
}

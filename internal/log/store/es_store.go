package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	logmodel "pim/internal/log/model"
)

// ESStore 封装 Elasticsearch 写入与查询。
type ESStore struct {
	baseURL string
	client  *http.Client
}

// NewESStore 创建 ES 存储。
func NewESStore(baseURL string) *ESStore {
	return &ESStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 8 * time.Second},
	}
}

func (s *ESStore) indexName(t time.Time) string {
	return fmt.Sprintf("logs-im-%04d.%02d.%02d", t.Year(), t.Month(), t.Day())
}

// Index 写入单条日志到按天索引。
func (s *ESStore) Index(ctx context.Context, e logmodel.Log) error {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/%s/_doc", s.baseURL, s.indexName(e.TS))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("index log failed: status=%d body=%s", resp.StatusCode, string(b))
	}
	return nil
}

// SearchByTraceID 查询 trace_id 日志。
func (s *ESStore) SearchByTraceID(ctx context.Context, traceID string, size int) ([]map[string]interface{}, error) {
	if size <= 0 {
		size = 200
	}
	query := map[string]interface{}{
		"size": size,
		"sort": []map[string]interface{}{{"ts": map[string]string{"order": "asc"}}},
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"trace_id": traceID,
			},
		},
	}
	return s.search(ctx, query)
}

// SearchByEventID 查询 event_id 日志。
func (s *ESStore) SearchByEventID(ctx context.Context, eventID string, size int) ([]map[string]interface{}, error) {
	if size <= 0 {
		size = 200
	}
	query := map[string]interface{}{
		"size": size,
		"sort": []map[string]interface{}{{"ts": map[string]string{"order": "asc"}}},
		"query": map[string]interface{}{
			"term": map[string]interface{}{
				"event_id": eventID,
			},
		},
	}
	return s.search(ctx, query)
}

// SearchByFilter 按 service/level/time range 组合过滤查询日志。
func (s *ESStore) SearchByFilter(ctx context.Context, service, level, start, end string, size int) ([]map[string]interface{}, error) {
	if size <= 0 {
		size = 200
	}
	if size > 2000 {
		size = 2000
	}
	must := make([]map[string]interface{}, 0, 4)
	if v := strings.TrimSpace(service); v != "" {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"service": v},
		})
	}
	if v := strings.TrimSpace(level); v != "" {
		must = append(must, map[string]interface{}{
			"term": map[string]interface{}{"level": strings.ToLower(v)},
		})
	}
	if strings.TrimSpace(start) != "" || strings.TrimSpace(end) != "" {
		rng := map[string]interface{}{}
		if strings.TrimSpace(start) != "" {
			rng["gte"] = strings.TrimSpace(start)
		}
		if strings.TrimSpace(end) != "" {
			rng["lte"] = strings.TrimSpace(end)
		}
		must = append(must, map[string]interface{}{
			"range": map[string]interface{}{
				"@timestamp": rng,
			},
		})
	}

	query := map[string]interface{}{
		"size": size,
		"sort": []map[string]interface{}{{"@timestamp": map[string]string{"order": "desc"}}},
		"query": map[string]interface{}{
			"bool": map[string]interface{}{
				"must": must,
			},
		},
	}
	return s.search(ctx, query)
}

func (s *ESStore) search(ctx context.Context, query map[string]interface{}) ([]map[string]interface{}, error) {
	b, err := s.postSearch(ctx, query)
	if err != nil {
		return nil, err
	}
	var out struct {
		Hits struct {
			Hits []struct {
				Source map[string]interface{} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	items := make([]map[string]interface{}, 0, len(out.Hits.Hits))
	for _, h := range out.Hits.Hits {
		items = append(items, h.Source)
	}
	return items, nil
}

// AdminMetrics 返回后台概览统计。
func (s *ESStore) AdminMetrics(ctx context.Context) (map[string]interface{}, error) {
	total, err := s.count(ctx, map[string]interface{}{"match_all": map[string]interface{}{}})
	if err != nil {
		return nil, err
	}
	last15m, err := s.count(ctx, map[string]interface{}{
		"range": map[string]interface{}{
			"@timestamp": map[string]interface{}{"gte": "now-15m"},
		},
	})
	if err != nil {
		return nil, err
	}
	aggQuery := map[string]interface{}{
		"size": 0,
		"aggs": map[string]interface{}{
			"by_level": map[string]interface{}{
				"terms": map[string]interface{}{"field": "level", "size": 10},
			},
			"by_service": map[string]interface{}{
				"terms": map[string]interface{}{"field": "service", "size": 20},
			},
		},
	}
	raw, err := s.postSearchMap(ctx, aggQuery)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"total_logs":    total,
		"last_15m_logs": last15m,
		"by_level":      parseTerms(raw, "by_level"),
		"by_service":    parseTerms(raw, "by_service"),
		"generated_at":  time.Now().Format(time.RFC3339),
		"es_base_url":   s.baseURL,
	}, nil
}

func (s *ESStore) count(ctx context.Context, query map[string]interface{}) (int64, error) {
	body := map[string]interface{}{"query": query}
	b, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	u := fmt.Sprintf("%s/logs-im-*/_count", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("count logs failed: status=%d body=%s", resp.StatusCode, string(raw))
	}
	var out struct {
		Count int64 `json:"count"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	return out.Count, nil
}

func (s *ESStore) postSearch(ctx context.Context, query map[string]interface{}) ([]byte, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/logs-im-*/_search", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search logs failed: status=%d body=%s", resp.StatusCode, string(b))
	}
	return b, nil
}

func (s *ESStore) postSearchMap(ctx context.Context, query map[string]interface{}) (map[string]interface{}, error) {
	b, err := s.postSearch(ctx, query)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseTerms(raw map[string]interface{}, name string) map[string]int64 {
	result := map[string]int64{}
	aggs, ok := raw["aggregations"].(map[string]interface{})
	if !ok {
		return result
	}
	target, ok := aggs[name].(map[string]interface{})
	if !ok {
		return result
	}
	buckets, ok := target["buckets"].([]interface{})
	if !ok {
		return result
	}
	for _, b := range buckets {
		item, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		key, _ := item["key"].(string)
		if key == "" {
			continue
		}
		switch v := item["doc_count"].(type) {
		case float64:
			result[key] = int64(v)
		case int64:
			result[key] = v
		case int:
			result[key] = int64(v)
		}
	}
	return result
}

// Health 检查 ES 可用性。
func (s *ESStore) Health(ctx context.Context) error {
	u, _ := url.JoinPath(s.baseURL, "_cluster/health")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("es health failed: status=%d body=%s", resp.StatusCode, string(b))
	}
	return nil
}

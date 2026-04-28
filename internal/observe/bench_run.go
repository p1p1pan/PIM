package observe

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// bench-run POST：按「工具 + measure」分槽，同一槽再次 POST 会覆盖。overview 的 bench_runs 为各槽快照。
// bench_run 仍为任意一次成功 POST 的最新副本（兼容旧前端）。进程重启后清空。

const maxBenchRunBody = 65536

var (
	benchRunMu       sync.Mutex
	lastBenchBySlot  = make(map[string]map[string]interface{}) // e.g. msg-latency:e2e, group-broadcast:ack
	lastBenchRunPost map[string]interface{}
)

func cloneBenchJSONMap(m map[string]interface{}) (interface{}, bool) {
	if m == nil {
		return nil, false
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, false
	}
	var out interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, false
	}
	return out, true
}

// benchSlotKey 返回 overview 里使用的槽位键与是否合法。
func benchSlotKey(schema, measure string) (string, bool) {
	switch measure {
	case "e2e", "ack":
	default:
		return "", false
	}
	switch schema {
	case "pim_bench_msg_latency_v1":
		return "msg-latency:" + measure, true
	case "pim_bench_group_broadcast_v1":
		return "group-broadcast:" + measure, true
	default:
		return "", false
	}
}

func (s *Service) handleAdminBenchRun(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBenchRunBody)
	b, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	sch, _ := m["schema"].(string)
	meas, _ := m["measure"].(string)
	slot, ok := benchSlotKey(sch, meas)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": `unsupported schema/measure (supported: pim_bench_msg_latency_v1|pim_bench_group_broadcast_v1 + measure e2e|ack)`,
		})
		return
	}
	benchRunMu.Lock()
	lastBenchBySlot[slot] = m
	lastBenchRunPost = m
	benchRunMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// snapshotBenchRun 返回最近一次 POST 的可 JSON 副本（无快照时为 nil）。
func snapshotBenchRun() interface{} {
	benchRunMu.Lock()
	defer benchRunMu.Unlock()
	v, ok := cloneBenchJSONMap(lastBenchRunPost)
	if !ok {
		return nil
	}
	return v
}

// snapshotBenchRuns 返回各槽最近一次结果（仅包含有数据的键）。
func snapshotBenchRuns() gin.H {
	benchRunMu.Lock()
	defer benchRunMu.Unlock()
	out := gin.H{}
	for slot, m := range lastBenchBySlot {
		if m == nil {
			continue
		}
		if v, ok := cloneBenchJSONMap(m); ok {
			out[slot] = v
		}
	}
	return out
}

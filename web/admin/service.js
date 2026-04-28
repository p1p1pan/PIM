/** 把 overview.bench_runs 规范成「槽位键 → 快照」；兼容旧接口仅有 e2e/ack 顶层的形状。 */
function normalizeBenchRunsPayload(raw) {
  if (!raw || typeof raw !== "object") return {};
  const keys = Object.keys(raw);
  const legacyOnly =
    keys.some((k) => k === "e2e" || k === "ack") &&
    !keys.some((k) => String(k).includes(":"));
  if (legacyOnly) {
    const o = {};
    if (raw.e2e && typeof raw.e2e === "object") o["msg-latency:e2e"] = raw.e2e;
    if (raw.ack && typeof raw.ack === "object") o["msg-latency:ack"] = raw.ack;
    return o;
  }
  const o = {};
  for (const k of keys) {
    const v = raw[k];
    if (v && typeof v === "object") o[k] = v;
  }
  return o;
}

/** 槽位键（如 msg-latency:e2e）→ 展示标签与样式 */
function benchSlotMeta(slotKey, row) {
  const m = String(row?.measure || "");
  const sk = String(slotKey || "");
  if (sk.startsWith("group-broadcast:")) {
    return {
      label: m === "e2e" ? "群聊 E2E" : "群聊 ACK",
      tagClass: m === "e2e" ? "bench-tag-e2e" : "bench-tag-ack",
      isGroup: true,
    };
  }
  if (sk.startsWith("msg-latency:")) {
    return {
      label: m === "e2e" ? "单聊 E2E" : "单聊 ACK",
      tagClass: m === "e2e" ? "bench-tag-e2e" : "bench-tag-ack",
      isGroup: false,
    };
  }
  return {
    label: m === "e2e" ? "E2E" : "ACK",
    tagClass: m === "e2e" ? "bench-tag-e2e" : "bench-tag-ack",
    isGroup: false,
  };
}

window.benchSlotMeta = benchSlotMeta;

window.buildAdminMethods = function buildAdminMethods() {
  return {
    backIM() {
      location.href = "../index.html";
    },
    async safeCall(fn, fallback) {
      this.error = "";
      this.loading = true;
      try {
        await fn();
      } catch (e) {
        this.error = `${fallback}: ${e.message}`;
      } finally {
        this.loading = false;
      }
    },
    switchMenu(menu) {
      this.activeMenu = menu;
      this.error = "";
      if (menu === "overview") {
        this.safeCall(() => this.loadMetrics(), "加载运行概览失败");
      }
      if (menu === "fileDlq") {
        this.safeCall(() => this.loadFileDlq(), "加载文件扫描死信失败");
      }
    },
    sparklinePath(series, width = 180, height = 44) {
      const arr = Array.isArray(series) ? series : [];
      if (arr.length === 0) return "";
      const vals = arr.map((x) => Number(x?.val || 0));
      const min = Math.min(...vals);
      const max = Math.max(...vals);
      const span = max - min || 1;
      const step = arr.length > 1 ? width / (arr.length - 1) : 0;
      const points = vals.map((v, i) => {
        const x = i * step;
        const y = height - ((v - min) / span) * height;
        return `${x.toFixed(2)},${y.toFixed(2)}`;
      });
      return `M ${points.join(" L ")}`;
    },
    sparklineMouseMove(evt, series, metricLabel = "value", unit = "") {
      const arr = Array.isArray(series) ? series : [];
      if (!arr.length) {
        this.sparkTooltip.show = false;
        return;
      }
      const rect = evt.currentTarget.getBoundingClientRect();
      const ratio = Math.min(1, Math.max(0, (evt.clientX - rect.left) / Math.max(1, rect.width)));
      const idx = Math.min(arr.length - 1, Math.max(0, Math.round((arr.length - 1) * ratio)));
      const p = arr[idx] || { ts: "-", val: 0 };
      const valueText = `${Number(p.val || 0).toFixed(3)}${unit ? ` ${unit}` : ""}`;
      this.sparkTooltip = {
        show: true,
        left: evt.clientX + 12,
        top: evt.clientY + 12,
        text: `${metricLabel}: ${valueText} | 时间: ${p.ts || "-"}`,
      };
    },
    sparklineMouseLeave() {
      this.sparkTooltip.show = false;
    },
    tsLatest(series, digits = 3) {
      const arr = Array.isArray(series) ? series : [];
      if (!arr.length) return "0";
      return Number(arr[arr.length - 1]?.val || 0).toFixed(digits);
    },
    formatTriplet(m) {
      if (!m || typeof m !== "object") return "-";
      return `${Number(m.avg).toFixed(2)} / ${Number(m.p95).toFixed(2)} / ${Number(m.p99).toFixed(2)}`;
    },
    formatFixed(v, digits) {
      if (v === null || v === undefined || Number.isNaN(Number(v))) return "—";
      return Number(v).toFixed(digits);
    },
    formatIsoLocal(s) {
      if (!s) return "—";
      const d = new Date(String(s));
      if (Number.isNaN(d.getTime())) return String(s);
      return d.toLocaleString();
    },
    onOverviewWindowChange() {
      this.safeCall(() => this.loadMetrics(), "切换时间窗口失败");
    },
    isUsefulAPIRoute(route) {
      const p = String(route || "").trim();
      if (!p) return false;
      // Hide low-value admin/health/drill endpoints from API quality display.
      if (p === "/api/v1/admin/health") return false;
      if (p === "/api/v1/admin/metrics") return false;
      if (p === "/api/v1/admin/observability/overview") return false;
      if (p.startsWith("/api/v1/admin/observability/drill/")) return false;
      return true;
    },
    apiVizMap() {
      const src = this.overview?.timeseries?.api_route || {};
      const out = {};
      for (const [route, series] of Object.entries(src)) {
        if (!this.isUsefulAPIRoute(route)) continue;
        out[route] = series;
      }
      return out;
    },
    apiVizKeys() {
      const m = this.apiVizMap();
      const all = Object.keys(m || {});
      const q = String(this.apiVizSearch || "").trim().toLowerCase();
      const filtered = q ? all.filter((k) => k.toLowerCase().includes(q)) : all;
      return filtered.sort((a, b) => a.localeCompare(b));
    },
    clearApiVizSelection() {
      this.apiVizSelectedKeys = [];
    },
    toggleApiVizKey(key) {
      const arr = Array.isArray(this.apiVizSelectedKeys) ? this.apiVizSelectedKeys : [];
      const idx = arr.indexOf(key);
      if (idx >= 0) {
        arr.splice(idx, 1);
      } else {
        arr.push(key);
      }
      this.apiVizSelectedKeys = [...arr];
    },
    apiVizSelectedItems() {
      const m = this.apiVizMap();
      const keys = Array.isArray(this.apiVizSelectedKeys) ? this.apiVizSelectedKeys : [];
      return keys
        .filter((k) => m && m[k])
        .map((k) => ({ key: k, series: m[k] || {} }));
    },
    startOverviewAutoRefresh() {
      if (this._overviewTimer) clearInterval(this._overviewTimer);
      if (!this.overviewAutoRefresh) return;
      this._overviewTimer = setInterval(() => {
        if (this.activeMenu !== "overview") return;
        this.safeCall(() => this.loadMetrics(), "自动刷新失败");
      }, 5000);
    },
    stopOverviewAutoRefresh() {
      if (this._overviewTimer) {
        clearInterval(this._overviewTimer);
        this._overviewTimer = null;
      }
    },
    apiQualityRows() {
      const rows = Array.isArray(this.overview?.api_quality) ? this.overview.api_quality : [];
      return rows.filter((r) => this.isUsefulAPIRoute(String(r?.route || "")));
    },
    apiQualityFilteredRows() {
      const rows = this.apiQualityRows();
      const keyword = String(this.apiQualityKeyword || "").trim().toLowerCase();
      const filtered = rows.filter((row) => {
        if (!keyword) return true;
        const route = String(row?.route || "").toLowerCase();
        return route.includes(keyword);
      });
      return filtered.sort((a, b) => Number(b?.qps || 0) - Number(a?.qps || 0));
    },
    apiQualityTotalPages() {
      const pageSize = Math.max(1, Number(this.apiQualityPageSize || 12));
      const total = this.apiQualityFilteredRows().length;
      return Math.max(1, Math.ceil(total / pageSize));
    },
    normalizeApiQualityPage() {
      const totalPages = this.apiQualityTotalPages();
      const current = Math.max(1, Number(this.apiQualityPage || 1));
      this.apiQualityPage = Math.min(current, totalPages);
    },
    apiQualityPagedRows() {
      this.normalizeApiQualityPage();
      const rows = this.apiQualityFilteredRows();
      const pageSize = Math.max(1, Number(this.apiQualityPageSize || 12));
      const start = (this.apiQualityPage - 1) * pageSize;
      return rows.slice(start, start + pageSize);
    },
    resetApiQualityPage() {
      this.apiQualityPage = 1;
    },
    prevApiQualityPage() {
      this.normalizeApiQualityPage();
      if (this.apiQualityPage > 1) this.apiQualityPage -= 1;
    },
    nextApiQualityPage() {
      this.normalizeApiQualityPage();
      const totalPages = this.apiQualityTotalPages();
      if (this.apiQualityPage < totalPages) this.apiQualityPage += 1;
    },
    updateLogStats() {
      const items = Array.isArray(this.items) ? this.items : [];
      const serviceSet = new Set();
      let err = 0;
      let warn = 0;
      for (const it of items) {
        const lvl = String(it?.level || "").toLowerCase();
        if (lvl === "error") err += 1;
        if (lvl === "warn" || lvl === "warning") warn += 1;
        const svc = String(it?.service || "").trim();
        if (svc) serviceSet.add(svc);
      }
      this.logStats = {
        total: items.length,
        error: err,
        warn,
        services: serviceSet.size,
      };
    },
    parseItems(res) {
      const r = res || {};
      if (Array.isArray(r.items)) {
        this.items = r.items;
        this.updateLogStats();
        return;
      }
      const nestedHits = r?.hits?.hits;
      if (Array.isArray(nestedHits)) {
        this.items = nestedHits.map((x) => x?._source || x).filter(Boolean);
        this.updateLogStats();
        return;
      }
      this.items = [];
      this.updateLogStats();
    },
    loadRecentTraceIDs() {
      this.recentTraceIDs = loadTraceHistory();
    },
    async pickRecentTrace(id) {
      this.traceID = String(id || "");
      await this.loadByTrace();
    },
    async loadByTrace() {
      const id = String(this.traceID || "").trim();
      if (!id) throw new Error("trace_id 不能为空");
      const res = await apiRequest(`/api/v1/logs/trace/${encodeURIComponent(id)}?size=${encodeURIComponent(this.size)}`);
      this.lastQuery = { kind: "trace", value: id };
      this.parseItems(res);
    },
    async loadByEvent() {
      const id = String(this.eventID || "").trim();
      if (!id) throw new Error("event_id 不能为空");
      const res = await apiRequest(`/api/v1/logs/search?event_id=${encodeURIComponent(id)}&size=${encodeURIComponent(this.size)}`);
      this.lastQuery = { kind: "event", value: id };
      this.parseItems(res);
    },
    async loadByFilter() {
      const params = new URLSearchParams();
      if (String(this.filterService || "").trim()) params.set("service", String(this.filterService).trim());
      if (String(this.filterLevel || "").trim()) params.set("level", String(this.filterLevel).trim());
      if (String(this.filterStart || "").trim()) params.set("start", String(this.filterStart).trim());
      if (String(this.filterEnd || "").trim()) params.set("end", String(this.filterEnd).trim());
      params.set("size", String(this.size || 200));
      const res = await apiRequest(`/api/v1/logs/filter?${params.toString()}`);
      this.lastQuery = {
        kind: "filter",
        value: JSON.stringify({
          service: this.filterService || "",
          level: this.filterLevel || "",
          start: this.filterStart || "",
          end: this.filterEnd || "",
        }),
      };
      this.parseItems(res);
    },
    async refreshLastQuery() {
      if (!this.lastQuery) return;
      if (this.lastQuery.kind === "trace") {
        this.traceID = this.lastQuery.value;
        await this.loadByTrace();
        return;
      }
      if (this.lastQuery.kind === "filter") {
        try {
          const obj = JSON.parse(this.lastQuery.value || "{}");
          this.filterService = obj.service || "";
          this.filterLevel = obj.level || "";
          this.filterStart = obj.start || "";
          this.filterEnd = obj.end || "";
        } catch (_) {}
        await this.loadByFilter();
        return;
      }
      this.eventID = this.lastQuery.value;
      await this.loadByEvent();
    },
    async loadMetrics() {
      let res;
      try {
        // Stage5 contract-first endpoint for frontend observability.
        const q = new URLSearchParams();
        q.set("window", String(this.overviewWindow || "15m"));
        res = await apiRequest(`/api/v1/admin/observability/overview?${q.toString()}`);
      } catch (_) {
        // Backward compatibility for older gateway versions.
        const legacy = await apiRequest("/api/v1/admin/metrics");
        res = {
          service_health: [],
          service_instances: [],
          service_health_summary: { up: 0, total: 0 },
          sources: {},
          scopes: {},
          api_quality: [],
          bench_im_ws: {},
          bench_run: null,
          bench_runs: {},
          message_pipeline: [],
          gateway_connections: { node: "gateway-1", active_connections: Number(legacy.online_users || 0), connect_rate: 0, disconnect_rate: 0 },
          timeseries: {
            window: String(this.overviewWindow || "15m"),
            ingress_qps: [],
            api_p95_ms: [],
            error_rate: [],
            push_success_rate: [],
            kafka_retry_total: [],
            api_domain: {},
            api_route: {},
          },
          downstream_write_quality: {
            gateway_ingress: { dispatch_p95_ms: 0, member_check_p95_ms: 0, ingress_total_p95_ms: 0 },
            kafka_write: { produce_total: 0, consume_total: 0, retry_like_total: 0, dlq_total: 0, handler_p95_ms: 0 },
            gateway_push: { ok_total: 0, fail_total: 0, success_rate: 0, delivery_p95_ms: 0 },
          },
          alerts_overview: { active_count: 0, severity: "none", source: "none", started_at: "" },
          slo_overview: {
            api_availability_target: 0.99,
            api_availability_now: 0,
            api_availability_met: false,
            api_p95_target_ms: 300,
            api_p95_now_ms: 0,
            api_p95_met: false,
            msg_e2e_p95_target_ms: 500,
            msg_e2e_p95_now_ms: 0,
            msg_e2e_p95_met: false,
          },
          generated_at: String(legacy.generated_at || ""),
          overview_meta: {},
        };
      }
      this.overview = {
        service_health: Array.isArray(res.service_health) ? res.service_health : [],
        service_instances: Array.isArray(res.service_instances) ? res.service_instances : [],
        service_health_summary: res.service_health_summary || { up: 0, total: 0 },
        api_quality: Array.isArray(res.api_quality) ? res.api_quality : [],
        bench_im_ws: res.bench_im_ws && typeof res.bench_im_ws === "object" ? res.bench_im_ws : {},
        bench_run: res.bench_run && typeof res.bench_run === "object" ? res.bench_run : null,
        bench_runs: normalizeBenchRunsPayload(res.bench_runs),
        message_pipeline: Array.isArray(res.message_pipeline) ? res.message_pipeline : [],
        gateway_connections: res.gateway_connections || { node: "gateway-1", active_connections: 0, connect_rate: 0, disconnect_rate: 0 },
        timeseries: res.timeseries || {
          window: String(this.overviewWindow || "15m"),
          ingress_qps: [],
          api_p95_ms: [],
          error_rate: [],
          push_success_rate: [],
          kafka_retry_total: [],
          api_domain: {},
          api_route: {},
        },
        downstream_write_quality: res.downstream_write_quality || {
          gateway_ingress: { dispatch_p95_ms: 0, member_check_p95_ms: 0, ingress_total_p95_ms: 0 },
          kafka_write: { produce_total: 0, consume_total: 0, retry_like_total: 0, dlq_total: 0, handler_p95_ms: 0 },
          gateway_push: { ok_total: 0, fail_total: 0, success_rate: 0, delivery_p95_ms: 0 },
        },
        alerts_overview: res.alerts_overview || { active_count: 0, severity: "none", source: "none", started_at: "" },
        slo_overview: res.slo_overview || {
          api_availability_target: 0.99,
          api_availability_now: 0,
          api_availability_met: false,
          api_p95_target_ms: 300,
          api_p95_now_ms: 0,
          api_p95_met: false,
          msg_e2e_p95_target_ms: 500,
          msg_e2e_p95_now_ms: 0,
          msg_e2e_p95_met: false,
        },
        sources: res.sources || {},
        scopes: res.scopes || {},
        overview_meta: res.overview_meta && typeof res.overview_meta === "object" ? res.overview_meta : {},
        generated_at: String(res.generated_at || ""),
      };
      this.normalizeApiQualityPage();
    },
    // 源徽标：优先 overview.sources[key]。"prom"= 集群 PromQL；"local"= observe 用本机快照（拉 gateway /metrics 或本进程），不是「网关进程内」。
    sourceLabel(key) {
      const src = String(this.overview?.sources?.[key] || "local");
      return src === "prom" ? "Prometheus" : "本机快照";
    },
    sourceBadgeClass(key) {
      const src = String(this.overview?.sources?.[key] || "local");
      return src === "prom" ? "src-prom" : "src-local";
    },
    scopeLabel(key) {
      const scope = String(this.overview?.scopes?.[key] || "local");
      return scope === "cluster" ? "集群" : "本机";
    },
    serviceInstancesByService() {
      const list = Array.isArray(this.overview?.service_instances) ? this.overview.service_instances : [];
      const m = {};
      for (const it of list) {
        const svc = String(it?.service || it?.job || "unknown");
        if (!m[svc]) m[svc] = [];
        m[svc].push(it);
      }
      return m;
    },
    serviceInstancesServices() {
      return Object.keys(this.serviceInstancesByService()).sort();
    },
    toggleServiceInstanceGroup(svc) {
      const cur = { ...(this.serviceInstanceOpen || {}) };
      cur[svc] = !cur[svc];
      this.serviceInstanceOpen = cur;
    },
    isServiceInstanceGroupOpen(svc) {
      return !!(this.serviceInstanceOpen && this.serviceInstanceOpen[svc]);
    },
    serviceInstanceGroupSummary(svc) {
      const list = this.serviceInstancesByService()[svc] || [];
      let up = 0;
      for (const it of list) if (it && it.up) up++;
      return { up, total: list.length };
    },
    async initAdminPage() {
      this.loadRecentTraceIDs();
      await this.safeCall(async () => {
        await this.loadMetrics();
      }, "初始化后台失败");
      this.startOverviewAutoRefresh();
    },
    async loadFileDlq() {
      const lim = Number(this.dlqLimit) || 20;
      const off = Number(this.dlqOffset) || 0;
      const res = await apiRequest(`/api/v1/admin/file-scan/dlq?limit=${encodeURIComponent(lim)}&offset=${encodeURIComponent(off)}`);
      this.dlqItems = Array.isArray(res.items) ? res.items : [];
    },
    async replayDlq(fileId) {
      const id = String(fileId || "").trim();
      if (!id) throw new Error("file_id 无效");
      await apiRequest(`/api/v1/admin/file-scan/dlq/${encodeURIComponent(id)}/replay`, { method: "POST" });
      await this.loadFileDlq();
    },
  };
};

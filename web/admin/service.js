window.buildAdminMethods = function buildAdminMethods() {
  return {
    backIM() {
      location.href = "./im.html";
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
      if (menu === "health") {
        this.safeCall(() => this.loadHealth(), "加载健康状态失败");
      }
      if (menu === "fileDlq") {
        this.safeCall(() => this.loadFileDlq(), "加载文件扫描死信失败");
      }
    },
    parseItems(res) {
      const r = res || {};
      if (Array.isArray(r.items)) {
        this.items = r.items;
        return;
      }
      const nestedHits = r?.hits?.hits;
      if (Array.isArray(nestedHits)) {
        this.items = nestedHits.map((x) => x?._source || x).filter(Boolean);
        return;
      }
      this.items = [];
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
    async loadHealth() {
      const res = await apiRequest("/api/v1/admin/health");
      this.healthGeneratedAt = String(res.generated_at || "");
      this.healthServices = Array.isArray(res.services) ? res.services : [];
    },
    async loadMetrics() {
      const res = await apiRequest("/api/v1/admin/metrics");
      this.overview = {
        online_users: Number(res.online_users || 0),
        total_logs: Number(res.total_logs || 0),
        last_15m_logs: Number(res.last_15m_logs || 0),
        by_level: res.by_level || {},
        by_service: res.by_service || {},
        generated_at: String(res.generated_at || ""),
      };
    },
    async initAdminPage() {
      this.loadRecentTraceIDs();
      await this.safeCall(async () => {
        await Promise.all([this.loadMetrics(), this.loadHealth()]);
      }, "初始化后台失败");
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

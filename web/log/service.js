window.buildLogMethods = function buildLogMethods() {
  return {
    backIM() {
      location.href = "./im.html";
    },
    async safeCall(fn, fallback) {
      try {
        await fn();
      } catch (e) {
        this.error = `${fallback}: ${e.message}`;
      }
    },
    parseItems(res) {
      const r = res || {};
      if (Array.isArray(r.items)) {
        this.items = r.items;
        return;
      }
      if (Array.isArray(r.hits)) {
        this.items = r.hits;
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
      this.error = "";
      this.parseItems(res);
    },
    async loadByEvent() {
      const id = String(this.eventID || "").trim();
      if (!id) throw new Error("event_id 不能为空");
      const res = await apiRequest(`/api/v1/logs/search?event_id=${encodeURIComponent(id)}&size=${encodeURIComponent(this.size)}`);
      this.lastQuery = { kind: "event", value: id };
      this.error = "";
      this.parseItems(res);
    },
    async refreshLastQuery() {
      if (!this.lastQuery) return;
      if (this.lastQuery.kind === "trace") {
        this.traceID = this.lastQuery.value;
        await this.loadByTrace();
        return;
      }
      this.eventID = this.lastQuery.value;
      await this.loadByEvent();
    },
    initLogPage() {
      this.loadRecentTraceIDs();
    },
  };
};

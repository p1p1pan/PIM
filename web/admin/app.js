const { createApp } = Vue;

createApp({
  data() {
    return {
      activeMenu: "overview",
      loading: false,
      error: "",
      // overview
      overview: {
        service_health: [],
        api_quality: [],
        message_pipeline: [],
        gateway_connections: { node: "", active_connections: 0, connect_rate: 0, disconnect_rate: 0 },
        timeseries: {
          window: "15m",
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
        generated_at: "",
      },
      overviewWindow: "15m",
      overviewAutoRefresh: true,
      apiVizSearch: "",
      apiVizSelectedKeys: [],
      apiVizPickerOpen: false,
      sparkTooltip: { show: false, left: 0, top: 0, text: "" },
      apiQualityKeyword: "",
      apiQualityPage: 1,
      apiQualityPageSize: 12,
      // logs
      traceID: "",
      eventID: "",
      filterService: "",
      filterLevel: "",
      filterStart: "",
      filterEnd: "",
      size: 200,
      items: [],
      logStats: { total: 0, error: 0, warn: 0, services: 0 },
      lastQuery: null,
      recentTraceIDs: [],
      // file-scan DLQ
      dlqItems: [],
      dlqLimit: 20,
      dlqOffset: 0,
    };
  },
  methods: window.buildAdminMethods(),
  mounted() {
    this.initAdminPage();
  },
  beforeUnmount() {
    if (typeof this.stopOverviewAutoRefresh === "function") this.stopOverviewAutoRefresh();
  },
  template: window.ADMIN_TEMPLATE,
}).mount("#admin-app");

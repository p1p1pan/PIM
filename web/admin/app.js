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
      // logs
      traceID: "",
      eventID: "",
      filterService: "",
      filterLevel: "",
      filterStart: "",
      filterEnd: "",
      size: 200,
      items: [],
      lastQuery: null,
      recentTraceIDs: [],
      // health
      healthGeneratedAt: "",
      healthServices: [],
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
  template: window.ADMIN_TEMPLATE,
}).mount("#admin-app");

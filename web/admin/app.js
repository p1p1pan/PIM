const { createApp } = Vue;

createApp({
  data() {
    return {
      activeMenu: "overview",
      loading: false,
      error: "",
      // overview
      overview: {
        online_users: 0,
        total_logs: 0,
        last_15m_logs: 0,
        by_level: {},
        by_service: {},
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

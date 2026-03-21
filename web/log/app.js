const { createApp } = Vue;

createApp({
  data() {
    return {
      traceID: "",
      eventID: "",
      size: 200,
      items: [],
      error: "",
      lastQuery: null,
      recentTraceIDs: [],
    };
  },
  methods: window.buildLogMethods(),
  mounted() {
    this.initLogPage();
  },
  template: window.LOG_TEMPLATE,
}).mount("#log-app");

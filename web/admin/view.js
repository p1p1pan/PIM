window.ADMIN_TEMPLATE = `
<div class="admin-layout">
  <aside class="left-nav">
    <h2>管理后台</h2>
    <button class="menu" :class="{active: activeMenu==='overview'}" @click="switchMenu('overview')">运行概览</button>
    <button class="menu" :class="{active: activeMenu==='logs'}" @click="switchMenu('logs')">日志中心</button>
    <button class="menu" :class="{active: activeMenu==='fileDlq'}" @click="switchMenu('fileDlq')">文件扫描死信</button>
    <button class="menu ghost" @click="backIM">返回入口</button>
  </aside>

  <section class="main" v-if="activeMenu==='overview'">
    <div class="panel full">
      <div class="head">
        <h3>运行概览</h3>
        <div>
          <label class="inline">窗口</label>
          <select class="inline-select" v-model="overviewWindow" @change="onOverviewWindowChange()">
            <option value="5m">5m</option>
            <option value="15m">15m</option>
            <option value="1h">1h</option>
            <option value="6h">6h</option>
            <option value="24h">24h</option>
          </select>
          <label class="inline checkbox-inline" style="margin-left:8px;">
            <input type="checkbox" v-model="overviewAutoRefresh" @change="overviewAutoRefresh ? startOverviewAutoRefresh() : stopOverviewAutoRefresh()" />
            自动刷新
          </label>
          <span class="muted">更新时间：{{ overview.generated_at || '-' }}</span>
          <button style="margin-left:10px" @click="safeCall(() => loadMetrics(), '刷新运行概览失败')">刷新</button>
        </div>
      </div>
      <div class="overview-cards">
        <div class="kpi">
          <div class="k">入口QPS</div>
          <div class="v">{{ tsLatest(overview.timeseries?.ingress_qps, 3) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.ingress_qps, '入口QPS', 'req/s')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.ingress_qps, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">API p95(ms)</div>
          <div class="v">{{ tsLatest(overview.timeseries?.api_p95_ms, 0) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.api_p95_ms, 'API P95', 'ms')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.api_p95_ms, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">错误率</div>
          <div class="v">{{ tsLatest(overview.timeseries?.error_rate, 4) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.error_rate, '错误率', 'ratio')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.error_rate, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">Push成功率(%)</div>
          <div class="v">{{ tsLatest(overview.timeseries?.push_success_rate, 2) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.push_success_rate, 'Push成功率', '%')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.push_success_rate, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">Kafka重试累计</div>
          <div class="v">{{ tsLatest(overview.timeseries?.kafka_retry_total, 0) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.kafka_retry_total, 'Kafka重试累计', 'count')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.kafka_retry_total, 180, 44)" /></svg>
        </div>
      </div>
      <div class="health-grid">
        <div class="health-card">
          <div class="row"><div class="name">告警状态</div><span class="badge" :class="(overview.alerts_overview.severity||'none')==='none' ? 'ok' : 'down'">{{ overview.alerts_overview.severity || 'none' }}</span></div>
          <div class="sub">source: {{ overview.alerts_overview.source || '-' }}</div>
          <div class="sub">started_at: {{ overview.alerts_overview.started_at || '-' }}</div>
          <div class="sub">gateway node: {{ overview.gateway_connections.node || '-' }}</div>
        </div>
        <div class="health-card">
          <div class="row"><div class="name">SLO 快照</div><span class="badge" :class="overview.slo_overview.api_availability_met ? 'ok' : 'down'">{{ overview.slo_overview.api_availability_met ? 'met' : 'not_met' }}</span></div>
          <div class="sub">availability target: {{ overview.slo_overview.api_availability_target }}</div>
          <div class="sub">api p95 now(ms): {{ overview.slo_overview.api_p95_now_ms || 0 }}</div>
          <div class="sub">api p95 target(ms): {{ overview.slo_overview.api_p95_target_ms }}</div>
          <div class="sub">msg e2e p95 now(ms): {{ overview.slo_overview.msg_e2e_p95_now_ms || 0 }}</div>
          <div class="sub">msg e2e p95 target(ms): {{ overview.slo_overview.msg_e2e_p95_target_ms }}</div>
          <div class="sub">msg e2e met: {{ overview.slo_overview.msg_e2e_p95_met ? 'yes' : 'no' }}</div>
        </div>
      </div>
      <div class="table-wrap">
        <div class="table-head">
          <h4>API 质量（可筛选分页）</h4>
          <span class="muted">当前 {{ apiQualityFilteredRows().length }} / 全部 {{ apiQualityRows().length }}</span>
        </div>
        <div class="muted">数据源：gateway 入口 HTTP 指标（非下游服务内部写入链路）</div>
        <div class="table-filters">
          <input v-model="apiQualityKeyword" @input="resetApiQualityPage()" placeholder="按路由关键词过滤，如 /api/v1/groups" />
          <label class="inline muted">每页</label>
          <input class="mini-input" v-model.number="apiQualityPageSize" @change="resetApiQualityPage()" type="number" min="5" max="100" />
        </div>
        <table class="simple-table" v-if="apiQualityRows().length">
          <thead><tr><th>route</th><th>qps</th><th>error_rate</th><th>p95(ms)</th></tr></thead>
          <tbody>
            <tr v-for="(row, idx) in apiQualityPagedRows()" :key="'aq-'+idx">
              <td>{{ row.route }}</td>
              <td>{{ Number(row.qps || 0).toFixed(3) }}</td>
              <td>{{ Number(row.error_rate || 0).toFixed(4) }}</td>
              <td>{{ row.p95_latency_ms || 0 }}</td>
            </tr>
          </tbody>
        </table>
        <div class="table-pager" v-if="apiQualityRows().length">
          <button class="small" :disabled="apiQualityPage<=1" @click="prevApiQualityPage()">上一页</button>
          <span class="muted">第 {{ apiQualityPage }} / {{ apiQualityTotalPages() }} 页</span>
          <button class="small" :disabled="apiQualityPage>=apiQualityTotalPages()" @click="nextApiQualityPage()">下一页</button>
        </div>
        <div class="empty" v-if="apiQualityRows().length && apiQualityFilteredRows().length===0">筛选结果为空，请调整条件</div>
        <div class="empty" v-if="!apiQualityRows().length">暂无 API 质量数据</div>
      </div>
      <div class="table-wrap">
        <div class="table-head">
          <h4>API 可视化图板</h4>
          <span class="muted">按路由分开选择（全量可选）</span>
        </div>
        <div class="api-viz-toolbar">
          <button class="small" @click="apiVizPickerOpen=!apiVizPickerOpen">{{ apiVizPickerOpen ? '收起路由选择器' : '展开路由选择器' }}</button>
          <button class="small" @click="clearApiVizSelection()">清空已选</button>
          <span class="muted">已选 {{ apiVizSelectedKeys.length }} 条</span>
        </div>
        <div class="viz-selected" v-if="apiVizSelectedKeys.length">
          <span class="chip" v-for="k in apiVizSelectedKeys" :key="'sel-chip-'+k">{{ k }}</span>
        </div>
        <div class="table-filters" v-if="apiVizPickerOpen">
          <input v-model="apiVizSearch" placeholder="搜索路由（如 /api/v1/friends）" />
        </div>
        <div class="viz-picker-list" v-if="apiVizPickerOpen">
          <label class="viz-row" v-for="k in apiVizKeys()" :key="'viz-'+k">
            <input type="checkbox" :checked="apiVizSelectedKeys.includes(k)" @change="toggleApiVizKey(k)" />
            <span>{{ k }}</span>
          </label>
        </div>
        <div class="overview-cards" v-if="apiVizSelectedItems().length">
          <div class="kpi" v-for="it in apiVizSelectedItems()" :key="'sel-'+it.key">
            <div class="k">{{ it.key }}</div>
            <div class="sub">qps: {{ tsLatest(it.series?.qps, 3) }} | err: {{ tsLatest(it.series?.error_rate, 4) }} | p95: {{ tsLatest(it.series?.p95_ms, 0) }}</div>
            <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, it.series?.qps, 'QPS', 'req/s')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(it.series?.qps, 180, 44)" /></svg>
            <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, it.series?.error_rate, '错误率', 'ratio')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(it.series?.error_rate, 180, 44)" /></svg>
            <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, it.series?.p95_ms, 'P95', 'ms')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(it.series?.p95_ms, 180, 44)" /></svg>
          </div>
        </div>
        <div class="empty" v-else>请选择上方任意路由查看趋势图</div>
      </div>
      <div class="table-wrap">
        <div class="table-head">
          <h4>Topic 明细</h4>
          <span class="muted">
            Kafka 总览：produce {{ overview.downstream_write_quality?.kafka_write?.produce_total || 0 }}
            / consume {{ overview.downstream_write_quality?.kafka_write?.consume_total || 0 }}
            / retry {{ overview.downstream_write_quality?.kafka_write?.retry_like_total || 0 }}
            / dlq {{ overview.downstream_write_quality?.kafka_write?.dlq_total || 0 }}
          </span>
        </div>
        <div class="overview-cards">
          <div class="kpi">
            <div class="k">Ingress p95(ms)</div>
            <div class="v">{{ overview.downstream_write_quality?.gateway_ingress?.ingress_total_p95_ms || 0 }}</div>
            <div class="sub">dispatch: {{ overview.downstream_write_quality?.gateway_ingress?.dispatch_p95_ms || 0 }} / member_check: {{ overview.downstream_write_quality?.gateway_ingress?.member_check_p95_ms || 0 }}</div>
          </div>
          <div class="kpi">
            <div class="k">Push 成功率</div>
            <div class="v">{{ Number((overview.downstream_write_quality?.gateway_push?.success_rate || 0) * 100).toFixed(2) }}%</div>
            <div class="sub">ok: {{ overview.downstream_write_quality?.gateway_push?.ok_total || 0 }} / fail: {{ overview.downstream_write_quality?.gateway_push?.fail_total || 0 }}</div>
          </div>
        </div>
        <table class="simple-table" v-if="overview.message_pipeline && overview.message_pipeline.length">
          <thead><tr><th>topic</th><th>produce_rate</th><th>consume_rate</th><th>produce_total</th><th>consume_total</th><th>retry_count</th><th>dlq_count</th></tr></thead>
          <tbody>
            <tr v-for="(row, idx) in overview.message_pipeline" :key="'mp-'+idx">
              <td>{{ row.topic }}</td>
              <td>{{ Number(row.produce_rate || 0).toFixed(3) }}</td>
              <td>{{ Number(row.consume_rate || 0).toFixed(3) }}</td>
              <td>{{ row.produce_total || 0 }}</td>
              <td>{{ row.consume_total || 0 }}</td>
              <td>{{ row.retry_count || 0 }}</td>
              <td>{{ row.dlq_count || 0 }}</td>
            </tr>
          </tbody>
        </table>
        <div class="empty" v-else>暂无消息链路数据</div>
      </div>
      <div class="table-wrap">
        <h4>服务健康快照</h4>
        <table class="simple-table" v-if="overview.service_health && overview.service_health.length">
          <thead><tr><th>service</th><th>up</th><th>error_rate</th><th>p95_latency_ms</th></tr></thead>
          <tbody>
            <tr v-for="(row, idx) in overview.service_health" :key="'sh-'+idx">
              <td>{{ row.service }}</td>
              <td>
                <span class="badge" :class="row.up ? 'ok' : 'down'">{{ row.up ? 'up' : 'down' }}</span>
              </td>
              <td>{{ Number(row.error_rate || 0).toFixed(4) }}</td>
              <td>{{ row.p95_latency_ms || 0 }}</td>
            </tr>
          </tbody>
        </table>
        <div class="empty" v-else>暂无服务健康快照</div>
      </div>
    </div>
  </section>

  <section class="main" v-else-if="activeMenu==='logs'">
    <div class="panel">
      <h3>日志筛选</h3>
      <label>最近 trace_id（点击即查）</label>
      <div class="trace-chips">
        <button class="chip" v-for="id in recentTraceIDs" :key="'t-'+id" @click="safeCall(() => pickRecentTrace(id), '查询最近 trace 失败')">{{ id }}</button>
        <div class="muted" v-if="recentTraceIDs.length===0">暂无自动记录</div>
      </div>
      <label>trace_id</label>
      <input v-model="traceID" placeholder="输入 trace_id 查询链路" />
      <button class="primary" @click="safeCall(() => loadByTrace(), '按 trace_id 查询失败')">按 Trace 查询</button>
      <label>event_id</label>
      <input v-model="eventID" placeholder="输入 event_id 查询事件" />
      <button @click="safeCall(() => loadByEvent(), '按 event_id 查询失败')">按 Event 查询</button>
      <label>service</label>
      <input v-model="filterService" placeholder="如 gateway / conversation-service" />
      <label>level</label>
      <input v-model="filterLevel" placeholder="如 info / warn / error" />
      <label>start</label>
      <input v-model="filterStart" placeholder="RFC3339，如 2026-03-17T10:00:00Z" />
      <label>end</label>
      <input v-model="filterEnd" placeholder="RFC3339，如 2026-03-17T11:00:00Z" />
      <button @click="safeCall(() => loadByFilter(), '按范围查询失败')">按范围查询</button>
      <label>size</label>
      <input v-model="size" type="number" min="1" max="2000" />
      <button @click="safeCall(() => refreshLastQuery(), '刷新失败')">刷新</button>
    </div>

    <div class="panel result">
      <div class="head">
        <h3>查询结果</h3>
        <span class="muted">共 {{ items.length }} 条</span>
      </div>
      <div class="overview-cards">
        <div class="kpi"><div class="k">总条数</div><div class="v">{{ logStats.total }}</div></div>
        <div class="kpi"><div class="k">错误</div><div class="v">{{ logStats.error }}</div></div>
        <div class="kpi"><div class="k">告警</div><div class="v">{{ logStats.warn }}</div></div>
        <div class="kpi"><div class="k">服务数</div><div class="v">{{ logStats.services }}</div></div>
      </div>
      <div class="list">
        <div class="item" v-for="(it, idx) in items" :key="'log-'+idx">
          <div class="line">
            <span class="lvl" :class="it.level || 'info'">{{ it.level || 'info' }}</span>
            <span class="svc">{{ it.service || '-' }}</span>
            <span class="ts">{{ it.ts || '-' }}</span>
          </div>
          <div class="msg">{{ it.msg || '-' }}</div>
          <div class="meta">
            <span>trace: {{ it.trace_id || '-' }}</span>
            <span>event: {{ it.event_id || '-' }}</span>
            <span>path: {{ it.path || '-' }}</span>
            <span>uid: {{ it.user_id || '-' }}</span>
            <span>gid: {{ it.group_id || '-' }}</span>
            <span>code: {{ it.error_code || '-' }}</span>
          </div>
          <details>
            <summary>原始字段</summary>
            <pre class="raw">{{ JSON.stringify(it, null, 2) }}</pre>
          </details>
        </div>
        <div class="empty" v-if="items.length===0">暂无日志数据</div>
      </div>
    </div>
  </section>

  <section class="main" v-else-if="activeMenu==='fileDlq'">
    <div class="panel full">
      <div class="head">
        <h3>文件扫描死信（DLQ）</h3>
        <div>
          <label class="inline">limit</label>
          <input class="inline-input" v-model.number="dlqLimit" type="number" min="1" max="200" />
          <label class="inline">offset</label>
          <input class="inline-input" v-model.number="dlqOffset" type="number" min="0" />
          <button style="margin-left:10px" @click="safeCall(() => loadFileDlq(), '刷新死信列表失败')">刷新</button>
        </div>
      </div>
      <p class="muted">来自 file-scan 超过重试次数的任务，可点「重放」重新投递扫描。</p>
      <div class="dlq-table-wrap">
        <table class="dlq-table" v-if="dlqItems.length">
          <thead>
            <tr>
              <th>file_id</th>
              <th>status</th>
              <th>result</th>
              <th>retry</th>
              <th>last_error</th>
              <th>dead_lettered_at</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in dlqItems" :key="'dlq-'+row.id">
              <td>{{ row.file_id }}</td>
              <td>{{ row.status }}</td>
              <td>{{ row.result || '-' }}</td>
              <td>{{ row.retry_count }}</td>
              <td class="cell-err">{{ row.last_error || '-' }}</td>
              <td>{{ row.dead_lettered_at || '-' }}</td>
              <td><button class="small" @click="safeCall(() => replayDlq(row.file_id), '重放失败')">重放</button></td>
            </tr>
          </tbody>
        </table>
        <div class="empty" v-else>暂无死信任务</div>
      </div>
    </div>
  </section>

  <div class="global-msg" v-if="loading || error">
    <span v-if="loading">加载中...</span>
    <span v-if="error" class="err">{{ error }}</span>
  </div>
  <div class="spark-tooltip" v-if="sparkTooltip.show" :style="{left: sparkTooltip.left + 'px', top: sparkTooltip.top + 'px'}">{{ sparkTooltip.text }}</div>
</div>
`;

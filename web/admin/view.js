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
      <div v-if="overview.overview_meta && (!overview.overview_meta.prometheus_reachable || !overview.overview_meta.gateway_metrics_scraped)" class="panel" style="margin:0 0 12px 0;padding:10px 12px;border-left:3px solid #6b6b7a">
        <div v-if="!overview.overview_meta.prometheus_reachable" class="sub" style="color:#c9c1b8;margin:0 0 6px 0">
          <b>未连上 Prometheus</b>（<code>PROMETHEUS_QUERY_URL</code> 默认 <code>http://127.0.0.1:9090</code>）。KPI 聚合、SLO/单聊·WS 卡片、API 表上 Prom 来源会缺失，多项可能为 0。请先启动 Prom 并保证 <b>observe 进程</b>能访问该地址。
        </div>
        <div v-if="!overview.overview_meta.gateway_metrics_scraped" class="sub" style="color:#c9c1b8;margin:0">
          <b>无法确认已拿到 gateway 的指标</b>：HTTP 拉取 <code>/metrics</code> 失败，且当前 mfs 中也没有 <code>pim_gateway_*</code> 等网关系列。请确认 <b>gateway 已启动</b>，且 <code>OBSERVE_GATEWAY_METRICS_SCRAPE_URL</code> 在运行 observe 的主机上可访问（如 <code>http://127.0.0.1:26080/metrics</code>）。
        </div>
        <p v-if="overview.overview_meta && overview.overview_meta.prometheus_reachable && !overview.overview_meta.prometheus_reachable_probe" class="muted" style="margin:8px 0 0 0;font-size:12px">说明：探针偶发未命中，但本页已根据 Prom 聚合；若数正常可忽略。默认 <code>PROMETHEUS_QUERY_URL</code> 为 <code>http://127.0.0.1:9090</code> 以降低 localhost/IPv6 问题。</p>
        <p class="muted" style="margin:8px 0 0 0;font-size:12px">徽标「本机快照」= 非 Prom 聚合的降级；由 observe 拉取 gateway 文本或本进程指标。Prom 侧是否可用以 <code>prometheus_reachable</code> 与 <code>sources</code> 一致为准。</p>
      </div>
      <div v-if="overview.overview_meta && overview.overview_meta.prometheus_reachable && Number(overview.overview_meta.prom_pim_gateway_targets_up) === 0" class="panel" style="margin:0 0 12px 0;padding:10px 12px;border-left:3px solid #a35d5d">
        <div class="sub" style="color:#e8c8c0;margin:0 0 6px 0">
          <b>已连上 Prometheus，但 <code>job=pim-gateway</code> 下所有实例均为 down</b>（容器内 <code>sum(up{job=&quot;pim-gateway&quot;}) = 0</code>）。此时 Prom 里<strong>没有</strong>可聚合的 <code>pim_http_*</code> / WS 直方图，压测有流量后台也可能仍显示 0。
        </div>
        <p class="muted" style="margin:0 0 6px 0;font-size:12px">请到 <a href="http://localhost:9090/targets" target="_blank" rel="noopener">http://localhost:9090/targets</a> 看 <b>pim-gateway</b> 的 <code>host.docker.internal:26080</code> / <code>26180</code> 是否 <b>Up</b>。Down 时多为：<b>Prom 跑在 Docker 里、gateway 跑在宿主机</b>，容器访问不到你的本机端口——需保证 Docker 能连宿主机（Windows Docker Desktop 一般可；也可改 Prom 的 static_configs 为真实可达 IP/端口）。</p>
        <p class="muted" style="margin:0;font-size:12px">若目标已为 Up 仍长时间为 0：可等待 1～2 分钟让 <code>rate(...[1m])</code> 有数据，或保持压测/请求流量。</p>
      </div>
      <div class="overview-cards">
        <div class="kpi">
          <div class="k">入口QPS <span class="src-badge" :class="sourceBadgeClass('ingress_qps')" :title="'来源: '+sourceLabel('ingress_qps')+' | 视角: '+scopeLabel('ingress_qps')">{{ sourceLabel('ingress_qps') }}</span></div>
          <div class="v">{{ tsLatest(overview.timeseries?.ingress_qps, 3) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.ingress_qps, '入口QPS', 'req/s')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.ingress_qps, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">API p95(ms) <span class="src-badge" :class="sourceBadgeClass('api_p95_ms')" :title="'来源: '+sourceLabel('api_p95_ms')+' | 视角: '+scopeLabel('api_p95_ms')">{{ sourceLabel('api_p95_ms') }}</span></div>
          <div class="v">{{ tsLatest(overview.timeseries?.api_p95_ms, 0) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.api_p95_ms, 'API P95', 'ms')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.api_p95_ms, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">错误率 <span class="src-badge" :class="sourceBadgeClass('error_rate')" :title="'来源: '+sourceLabel('error_rate')+' | 视角: '+scopeLabel('error_rate')">{{ sourceLabel('error_rate') }}</span></div>
          <div class="v">{{ tsLatest(overview.timeseries?.error_rate, 4) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.error_rate, '错误率', 'ratio')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.error_rate, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">Push成功率(%) <span class="src-badge" :class="sourceBadgeClass('push_success_rate')" :title="'来源: '+sourceLabel('push_success_rate')+' | 视角: '+scopeLabel('push_success_rate')">{{ sourceLabel('push_success_rate') }}</span></div>
          <div class="v">{{ tsLatest(overview.timeseries?.push_success_rate, 2) }}</div>
          <svg class="sparkline" viewBox="0 0 180 44" preserveAspectRatio="none" @mousemove="sparklineMouseMove($event, overview.timeseries?.push_success_rate, 'Push成功率', '%')" @mouseleave="sparklineMouseLeave()"><path :d="sparklinePath(overview.timeseries?.push_success_rate, 180, 44)" /></svg>
        </div>
        <div class="kpi">
          <div class="k">Kafka重试累计 <span class="src-badge" :class="sourceBadgeClass('kafka_retry_total')" :title="'来源: '+sourceLabel('kafka_retry_total')+' | 视角: '+scopeLabel('kafka_retry_total')">{{ sourceLabel('kafka_retry_total') }}</span></div>
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
          <div class="row">
            <div class="name">SLO 快照 <span class="src-badge" :class="sourceBadgeClass('slo_overview')" :title="'来源: '+sourceLabel('slo_overview')">{{ sourceLabel('slo_overview') }}</span></div>
            <span class="badge" :class="overview.slo_overview.api_availability_met ? 'ok' : 'down'">{{ overview.slo_overview.api_availability_met ? 'met' : 'not_met' }}</span>
          </div>
          <div class="sub">availability target: {{ overview.slo_overview.api_availability_target }}</div>
          <div class="sub">api p95 now(ms): {{ overview.slo_overview.api_p95_now_ms || 0 }}</div>
          <div class="sub">api p95 target(ms): {{ overview.slo_overview.api_p95_target_ms }}</div>
          <div class="sub">msg e2e p95 now(ms): {{ overview.slo_overview.msg_e2e_p95_now_ms || 0 }}</div>
          <div class="sub">msg e2e p95 target(ms): {{ overview.slo_overview.msg_e2e_p95_target_ms }}</div>
          <div class="sub">msg e2e met: {{ overview.slo_overview.msg_e2e_p95_met ? 'yes' : 'no' }}</div>
          <div class="sub" v-if="overview.slo_overview.ws_ack_p95_now_ms !== undefined">ws ack p95 now(ms): {{ overview.slo_overview.ws_ack_p95_now_ms || 0 }} / target: {{ overview.slo_overview.ws_ack_p95_target_ms || 100 }} <span class="badge" :class="overview.slo_overview.ws_ack_p95_met ? 'ok' : 'down'">{{ overview.slo_overview.ws_ack_p95_met ? 'met' : 'not_met' }}</span></div>
          <div class="sub" v-if="overview.slo_overview.ws_ack_p99_now_ms !== undefined && overview.slo_overview.ws_ack_p99_now_ms > 0">ws ack p99 now(ms): {{ overview.slo_overview.ws_ack_p99_now_ms || 0 }}</div>
          <div class="sub" v-if="overview.slo_overview.msg_e2e_p99_now_ms !== undefined && overview.slo_overview.msg_e2e_p99_now_ms > 0">msg e2e p99 now(ms): {{ overview.slo_overview.msg_e2e_p99_now_ms || 0 }}</div>
        </div>
      </div>
      <div class="table-wrap bench-run-section">
        <div class="table-head bench-run-head">
          <h4>压测快照 <span class="bench-run-title-sub">各工具 × 模式各保留最近一次（同槽覆盖）</span></h4>
        </div>
        <p class="muted bench-run-hint">msg-latency / group-broadcast 成功后写入 observe（内存，重启清空；不读 Prom）。详见 <code>test/README.md</code>。</p>
        <div v-for="p in benchRunPanels" :key="p.slotKey" class="bench-run-panel">
          <div class="bench-run-meta">
            <span class="bench-tag" :class="p.tagClass">{{ p.label }}</span>
            <span v-if="p.row.bench_name" class="bench-test-name">{{ p.row.bench_name }}</span>
            <span v-else-if="p.row.prefix" class="bench-test-name fallback"><code>{{ p.row.prefix }}</code></span>
            <span class="bench-time">{{ formatIsoLocal(p.row.finished_at) }}</span>
          </div>
          <div class="bench-run-foot" v-if="p.row.gateway">
            <span class="bench-gateway">{{ p.row.gateway }}</span>
          </div>
          <div class="bench-run-stats">
            <div class="bench-stat">
              <div class="bench-stat-label">总耗时</div>
              <div class="bench-stat-value">{{ formatFixed(p.row.total_elapsed_sec, 3) }}<span class="unit">s</span></div>
            </div>
            <div class="bench-stat">
              <div class="bench-stat-label">有效样本</div>
              <div class="bench-stat-value">{{ p.row.sample_count != null ? p.row.sample_count : '—' }}</div>
            </div>
            <div class="bench-stat">
              <div class="bench-stat-label">期望发送</div>
              <div class="bench-stat-value">{{ p.row.expected_sends != null ? p.row.expected_sends : '—' }}</div>
            </div>
            <div class="bench-stat">
              <div class="bench-stat-label">吞吐</div>
              <div class="bench-stat-value">{{ formatFixed(p.row.qps, 2) }}<span class="unit">条/s</span></div>
            </div>
            <div class="bench-stat">
              <div class="bench-stat-label">用户数 n</div>
              <div class="bench-stat-value">{{ p.row.n != null ? p.row.n : '—' }}</div>
            </div>
            <div class="bench-stat" v-if="p.row.msgs_per_pair != null">
              <div class="bench-stat-label">每对条数</div>
              <div class="bench-stat-value">{{ p.row.msgs_per_pair }}</div>
            </div>
            <div class="bench-stat" v-if="p.isGroup && p.row.groups_total != null">
              <div class="bench-stat-label">群数</div>
              <div class="bench-stat-value">{{ p.row.groups_total }}</div>
            </div>
            <div class="bench-stat" v-if="p.isGroup && p.row.group_size != null">
              <div class="bench-stat-label">每群人数</div>
              <div class="bench-stat-value">{{ p.row.group_size }}</div>
            </div>
            <div class="bench-stat" v-if="p.isGroup && p.row.send_concurrent != null">
              <div class="bench-stat-label">发送并发上限</div>
              <div class="bench-stat-value">{{ p.row.send_concurrent }}</div>
            </div>
            <div class="bench-stat" v-if="p.isGroup && p.row.senders_per_group != null">
              <div class="bench-stat-label">每群发送者数</div>
              <div class="bench-stat-value">{{ p.row.senders_per_group }}</div>
            </div>
          </div>
          <div class="bench-latency" v-if="p.row.ack_ms || p.row.e2e_ms">
            <div class="bench-latency-title">{{ p.label }} 延迟（ms，客户端计时）</div>
            <div class="bench-lat-cols" v-if="p.row.ack_ms">
              <div class="bench-lat-item"><span class="lat-n">平均</span><span class="lat-v">{{ formatFixed(p.row.ack_ms.avg, 2) }}</span></div>
              <div class="bench-lat-item"><span class="lat-n">P95</span><span class="lat-v">{{ formatFixed(p.row.ack_ms.p95, 2) }}</span></div>
              <div class="bench-lat-item"><span class="lat-n">P99</span><span class="lat-v">{{ formatFixed(p.row.ack_ms.p99, 2) }}</span></div>
            </div>
            <div class="bench-lat-cols" v-else-if="p.row.e2e_ms">
              <div class="bench-lat-item"><span class="lat-n">平均</span><span class="lat-v">{{ formatFixed(p.row.e2e_ms.avg, 2) }}</span></div>
              <div class="bench-lat-item"><span class="lat-n">P95</span><span class="lat-v">{{ formatFixed(p.row.e2e_ms.p95, 2) }}</span></div>
              <div class="bench-lat-item"><span class="lat-n">P99</span><span class="lat-v">{{ formatFixed(p.row.e2e_ms.p99, 2) }}</span></div>
            </div>
          </div>
        </div>
        <div v-if="!benchRunPanels.length" class="bench-run-empty">暂无。压测成功 POST 到 observe（默认 26280）后点刷新。</div>
      </div>
      <div class="table-wrap">
        <div class="table-head">
          <h4>API 质量（可筛选分页）</h4>
          <span class="muted">当前 {{ apiQualityFilteredRows().length }} / 全部 {{ apiQualityRows().length }}</span>
        </div>
        <div class="muted">数据源：gateway 入口 HTTP 指标。上方「压测快照」为客户端 POST 的固定结果；勿用本表当 WS 消息主吞吐。</div>
        <div class="table-filters">
          <input v-model="apiQualityKeyword" @input="resetApiQualityPage()" placeholder="按路由关键词过滤，如 /api/v1/groups" />
          <label class="inline muted">每页</label>
          <input class="mini-input" v-model.number="apiQualityPageSize" @change="resetApiQualityPage()" type="number" min="5" max="100" />
        </div>
        <table class="simple-table" v-if="apiQualityRows().length">
          <thead><tr><th>route</th><th>qps</th><th>error_rate</th><th>p95(ms)</th><th>p99(ms)</th></tr></thead>
          <tbody>
            <tr v-for="(row, idx) in apiQualityPagedRows()" :key="'aq-'+idx">
              <td>{{ row.route }}</td>
              <td>{{ Number(row.qps || 0).toFixed(3) }}</td>
              <td>{{ Number(row.error_rate || 0).toFixed(4) }}</td>
              <td>{{ row.p95_latency_ms || 0 }}</td>
              <td>{{ row.p99_latency_ms != null && row.p99_latency_ms !== undefined ? row.p99_latency_ms : '-' }}</td>
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
            <div class="sub">qps: {{ tsLatest(it.series?.qps, 3) }} | err: {{ tsLatest(it.series?.error_rate, 4) }} | p95: {{ tsLatest(it.series?.p95_ms, 0) }}<template v-if="it.series?.p99_ms && it.series.p99_ms.length"> | p99: {{ tsLatest(it.series.p99_ms, 0) }}</template></div>
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
        <div class="table-head">
          <h4>服务健康快照</h4>
          <span class="muted" v-if="(overview.service_health_summary?.total || 0) > 0">
            集群 up: <b>{{ overview.service_health_summary.up }}</b> / {{ overview.service_health_summary.total }}
            <span class="src-badge" :class="sourceBadgeClass('service_instances')" :title="'来源: '+sourceLabel('service_instances')">{{ sourceLabel('service_instances') }}</span>
          </span>
          <span class="muted" v-else>
            当前网关视角
            <span class="src-badge" :class="sourceBadgeClass('service_health')" :title="'来源: '+sourceLabel('service_health')">{{ sourceLabel('service_health') }}</span>
          </span>
        </div>
        <!-- 本网关视角（HTTP 探测结果） -->
        <table class="simple-table" v-if="overview.service_health && overview.service_health.length">
          <thead><tr><th>service (本网关视角)</th><th>up</th><th>error_rate</th><th>p95_latency_ms</th></tr></thead>
          <tbody>
            <tr v-for="(row, idx) in overview.service_health" :key="'sh-'+idx">
              <td>{{ row.service }}</td>
              <td><span class="badge" :class="row.up ? 'ok' : 'down'">{{ row.up ? 'up' : 'down' }}</span></td>
              <td>{{ Number(row.error_rate || 0).toFixed(4) }}</td>
              <td>{{ row.p95_latency_ms || 0 }}</td>
            </tr>
          </tbody>
        </table>
        <!-- 集群 per-instance 视角（Prom up{job=~"pim-.*"}） -->
        <div v-if="(overview.service_instances || []).length" class="table-head" style="margin-top:10px;">
          <h4>集群实例展开 <span class="src-badge" :class="sourceBadgeClass('service_instances')">{{ sourceLabel('service_instances') }}</span></h4>
        </div>
        <div v-if="(overview.service_instances || []).length">
          <div v-for="svc in serviceInstancesServices()" :key="'svc-grp-'+svc" class="instance-group">
            <button class="small instance-toggle" @click="toggleServiceInstanceGroup(svc)">
              <span>{{ isServiceInstanceGroupOpen(svc) ? '▾' : '▸' }} {{ svc }}</span>
              <span class="muted" style="margin-left:8px;">up {{ serviceInstanceGroupSummary(svc).up }} / {{ serviceInstanceGroupSummary(svc).total }}</span>
            </button>
            <table class="simple-table" v-if="isServiceInstanceGroupOpen(svc)">
              <thead><tr><th>job</th><th>instance</th><th>up</th></tr></thead>
              <tbody>
                <tr v-for="(row, idx) in serviceInstancesByService()[svc]" :key="'si-'+svc+'-'+idx">
                  <td>{{ row.job }}</td>
                  <td>{{ row.instance }}</td>
                  <td><span class="badge" :class="row.up ? 'ok' : 'down'">{{ row.up ? 'up' : 'down' }}</span></td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
        <div class="empty" v-if="!(overview.service_health && overview.service_health.length) && !((overview.service_instances || []).length)">暂无服务健康快照</div>
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

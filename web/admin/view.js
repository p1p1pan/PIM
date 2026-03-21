window.ADMIN_TEMPLATE = `
<div class="admin-layout">
  <aside class="left-nav">
    <h2>管理后台</h2>
    <button class="menu" :class="{active: activeMenu==='overview'}" @click="switchMenu('overview')">运行概览</button>
    <button class="menu" :class="{active: activeMenu==='logs'}" @click="switchMenu('logs')">日志中心</button>
    <button class="menu" :class="{active: activeMenu==='fileDlq'}" @click="switchMenu('fileDlq')">文件扫描死信</button>
    <button class="menu" :class="{active: activeMenu==='health'}" @click="switchMenu('health')">服务健康</button>
    <button class="menu ghost" @click="backIM">返回IM</button>
  </aside>

  <section class="main" v-if="activeMenu==='overview'">
    <div class="panel full">
      <div class="head">
        <h3>运行概览</h3>
        <div>
          <span class="muted">更新时间：{{ overview.generated_at || '-' }}</span>
          <button style="margin-left:10px" @click="safeCall(() => loadMetrics(), '刷新运行概览失败')">刷新</button>
        </div>
      </div>
      <div class="overview-cards">
        <div class="kpi"><div class="k">在线连接</div><div class="v">{{ overview.online_users }}</div></div>
        <div class="kpi"><div class="k">日志总量</div><div class="v">{{ overview.total_logs }}</div></div>
        <div class="kpi"><div class="k">近15分钟日志</div><div class="v">{{ overview.last_15m_logs }}</div></div>
      </div>
      <div class="health-grid">
        <div class="health-card">
          <div class="row"><div class="name">按级别分布</div></div>
          <div class="sub" v-for="(v,k) in overview.by_level" :key="'lvl-'+k">{{ k }}: {{ v }}</div>
          <div class="sub" v-if="Object.keys(overview.by_level || {}).length===0">暂无数据</div>
        </div>
        <div class="health-card">
          <div class="row"><div class="name">按服务分布</div></div>
          <div class="sub" v-for="(v,k) in overview.by_service" :key="'svc-'+k">{{ k }}: {{ v }}</div>
          <div class="sub" v-if="Object.keys(overview.by_service || {}).length===0">暂无数据</div>
        </div>
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

  <section class="main" v-else>
    <div class="panel full">
      <div class="head">
        <h3>服务健康</h3>
        <div>
          <span class="muted">更新时间：{{ healthGeneratedAt || '-' }}</span>
          <button style="margin-left:10px" @click="safeCall(() => loadHealth(), '刷新健康状态失败')">刷新</button>
        </div>
      </div>
      <div class="health-grid">
        <div class="health-card" v-for="s in healthServices" :key="s.id">
          <div class="row">
            <div class="name">{{ s.name }}</div>
            <span class="badge" :class="s.status==='up' ? 'ok' : 'down'">{{ s.status || 'unknown' }}</span>
          </div>
          <div class="sub">URL: {{ s.url }}</div>
          <div class="sub">延迟: {{ s.latency_ms }} ms</div>
          <div class="err" v-if="s.error">{{ s.error }}</div>
        </div>
      </div>
    </div>
  </section>

  <div class="global-msg" v-if="loading || error">
    <span v-if="loading">加载中...</span>
    <span v-if="error" class="err">{{ error }}</span>
  </div>
</div>
`;

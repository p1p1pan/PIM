window.LOG_TEMPLATE = `
<div class="log-layout">
  <aside class="left">
    <h2>日志筛选</h2>
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
    <label>size</label>
    <input v-model="size" type="number" min="1" max="2000" />
    <button @click="safeCall(() => refreshLastQuery(), '刷新失败')">刷新</button>
    <div class="err" v-if="error">{{ error }}</div>
    <button @click="backIM">返回IM</button>
  </aside>
  <section class="right">
    <div class="head">
      <h2>查询结果</h2>
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
      <div class="empty" v-if="items.length===0">
        当前查询暂无日志。若刚触发请求，可点一次“刷新”；若仍为空，可能是该链路日志被采样策略过滤。
      </div>
    </div>
  </section>
</div>
`;

const { createApp } = Vue;
const LS_GROUPS_KEY = "pim_groups_local";
const LS_COLLAPSE_KEY = "pim_collapse_state";

createApp({
  data() {
    return {
      apiBase: "http://localhost:8080",
      token: null,
      currentUser: null,
      ws: null,
      wsStatus: "未连接",
      httpStatus: "未检测",

      registerForm: { username: "", password: "" },
      loginForm: { username: "", password: "" },
      collapse: {
        auth: true,
        status: true,
        friendReq: true,
        contacts: false,
        group: false,
      },

      friends: [],
      conversations: [],
      unreadMap: {},
      chatMode: "private",
      currentPeerId: null,
      messages: [],
      msgInput: "",

      friendReqForm: { toUserID: "" },
      incomingRequests: [],
      outgoingRequests: [],

      groupForm: { name: "", memberUserIDs: "" },
      groupCreateMemberInput: "",
      groupCreateMembers: [],
      groupsLocal: [],
      selectedGroupId: "",
      groupMembers: [],
      groupMemberForm: { targetUserID: "" },
      groupRemoveUserID: "",
      groupMsg: "",
      groupMessagesMap: {},

      debugLogs: [],
    };
  },
  computed: {
    headerUser() {
      if (!this.currentUser) return "未登录";
      return `已登录：${this.currentUser.username} (ID: ${this.currentUser.id})`;
    },
    currentChatTitle() {
      if (this.chatMode === "group") {
        const gid = Number(this.selectedGroupId);
        if (!gid) return "群聊（未选择群）";
        const g = this.groupsLocal.find((x) => Number(x.id) === gid);
        return g ? `${g.name} (#${g.id})` : `群 #${gid}`;
      }
      if (!this.currentPeerId) return "单聊（未选择好友）";
      return `用户 #${this.currentPeerId}`;
    },
    canSendPrivate() {
      return !!this.currentPeerId && !!this.msgInput.trim() && this.ws && this.ws.readyState === WebSocket.OPEN;
    },
    canSendGroup() {
      return !!Number(this.selectedGroupId) && !!this.groupMsg.trim();
    },
    sessionItems() {
      const privateSessions = new Map();
      for (const c of this.conversations || []) {
        const peerID = Number(c.peer_id || 0);
        if (!peerID) continue;
        privateSessions.set(`private:${peerID}`, {
          key: `private:${peerID}`,
          type: "private",
          id: peerID,
          title: `用户 #${peerID}`,
          unread: Number(this.unreadMap[peerID] || 0),
          ts: Number(c.updated_at || c.last_message_at || c.id || 0),
        });
      }
      for (const f of this.friends || []) {
        const peerID = Number(f.friend_id || 0);
        if (!peerID) continue;
        const key = `private:${peerID}`;
        if (!privateSessions.has(key)) {
          privateSessions.set(key, {
            key,
            type: "private",
            id: peerID,
            title: `用户 #${peerID}`,
            unread: Number(this.unreadMap[peerID] || 0),
            ts: 0,
          });
        }
      }
      const groups = (this.groupsLocal || []).map((g) => ({
        key: `group:${Number(g.id)}`,
        type: "group",
        id: Number(g.id),
        title: g.name || `群 #${g.id}`,
        unread: 0,
        ts: Number(g.updated_at || g.created_at || g.id || 0),
      }));
      return [...privateSessions.values(), ...groups].sort((a, b) => b.ts - a.ts);
    },
    currentSessionKey() {
      if (this.chatMode === "group" && Number(this.selectedGroupId)) return `group:${Number(this.selectedGroupId)}`;
      if (this.chatMode === "private" && Number(this.currentPeerId)) return `private:${Number(this.currentPeerId)}`;
      return "";
    },
  },
  methods: {
    togglePanel(key) {
      this.collapse[key] = !this.collapse[key];
      this.persistCollapseState();
    },
    selectGroup(gid) {
      this.chatMode = "group";
      this.currentPeerId = null;
      this.selectedGroupId = String(gid);
      if (!this.groupMessagesMap[Number(gid)]) this.groupMessagesMap[Number(gid)] = [];
      this.loadGroupMembers();
    },
    selectSession(item) {
      if (!item) return;
      if (item.type === "group") {
        this.selectGroup(item.id);
      } else {
        this.selectPeer(item.id);
      }
    },
    persistGroupDrafts() {
      try {
        localStorage.setItem(LS_GROUPS_KEY, JSON.stringify(this.groupsLocal));
      } catch (_) {}
    },
    restoreGroupDrafts() {
      try {
        const raw = localStorage.getItem(LS_GROUPS_KEY);
        if (!raw) return;
        const arr = JSON.parse(raw);
        if (Array.isArray(arr)) this.groupsLocal = arr;
      } catch (_) {}
    },
    persistCollapseState() {
      try {
        localStorage.setItem(LS_COLLAPSE_KEY, JSON.stringify(this.collapse));
      } catch (_) {}
    },
    restoreCollapseState() {
      try {
        const raw = localStorage.getItem(LS_COLLAPSE_KEY);
        if (!raw) return;
        const state = JSON.parse(raw);
        if (!state || typeof state !== "object") return;
        this.collapse = { ...this.collapse, ...state };
      } catch (_) {}
    },
    log(msg) {
      const time = new Date().toLocaleTimeString();
      this.debugLogs.push(`[${time}] ${msg}`);
      if (this.debugLogs.length > 400) this.debugLogs.shift();
    },
    async apiRequest(path, options = {}) {
      const headers = { "Content-Type": "application/json", ...(options.headers || {}) };
      if (this.token) headers.Authorization = `Bearer ${this.token}`;
      const resp = await fetch(this.apiBase + path, {
        method: options.method || "GET",
        headers,
        body: options.body ? JSON.stringify(options.body) : undefined,
      });
      const text = await resp.text();
      let data = null;
      try { data = text ? JSON.parse(text) : null; } catch (_) { data = null; }
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      return data;
    },
    genClientMsgID() {
      if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
      return `c_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
    },

    async pingHealth() {
      try {
        const r = await fetch(this.apiBase + "/health");
        this.httpStatus = r.ok ? "200 /health" : "错误";
      } catch (e) {
        this.httpStatus = "连接失败";
        this.log("GET /health 失败: " + e.message);
      }
    },
    async register() {
      try {
        await this.apiRequest("/api/v1/register", { method: "POST", body: this.registerForm });
        this.log("注册成功");
      } catch (e) {
        this.log("注册失败: " + e.message);
      }
    },
    async login() {
      try {
        const res = await this.apiRequest("/api/v1/login", { method: "POST", body: this.loginForm });
        this.token = res.token;
        this.currentUser = res.user;
        this.log("登录成功");
        await Promise.all([this.loadFriends(), this.loadConversations(), this.loadFriendRequestsCenter()]);
        this.connectWs();
      } catch (e) {
        this.log("登录失败: " + e.message);
      }
    },
    logout() {
      this.token = null;
      this.currentUser = null;
      this.friends = [];
      this.conversations = [];
      this.currentPeerId = null;
      this.messages = [];
      this.incomingRequests = [];
      this.outgoingRequests = [];
      this.groupMembers = [];
      if (this.ws) {
        try { this.ws.close(); } catch (_) {}
      }
      this.ws = null;
      this.wsStatus = "未连接";
      this.log("已退出登录");
    },

    async loadFriends() {
      try {
        const res = await this.apiRequest("/api/v1/friends");
        this.friends = res.friends || [];
      } catch (e) {
        this.log("获取好友失败: " + e.message);
      }
    },
    async loadConversations() {
      try {
        const res = await this.apiRequest("/api/v1/conversations");
        this.conversations = res.conversations || [];
      } catch (e) {
        this.log("获取会话失败: " + e.message);
      }
    },
    async selectPeer(peerId) {
      this.chatMode = "private";
      this.currentPeerId = peerId;
      this.selectedGroupId = "";
      this.messages = [];
      try {
        const res = await this.apiRequest(`/api/v1/messages?with=${encodeURIComponent(peerId)}`);
        const msgs = res.messages || [];
        this.unreadMap[peerId] = 0;
        this.messages = msgs.map((m) => ({
          content: m.content,
          isMe: this.currentUser && m.from_user_id === this.currentUser.id,
        }));
        try {
          await this.apiRequest(`/api/v1/conversations/${encodeURIComponent(peerId)}/read`, { method: "PUT" });
        } catch (e) {
          this.log("上报已读失败: " + e.message);
        }
      } catch (e) {
        this.log("拉取历史失败: " + e.message);
      }
    },
    connectWs() {
      if (!this.token) return this.log("请先登录");
      if (this.ws && this.ws.readyState === WebSocket.OPEN) return this.log("WebSocket 已连接");
      this.ws = new WebSocket(`ws://localhost:8080/ws?token=${encodeURIComponent(this.token)}`);
      this.ws.onopen = () => { this.wsStatus = "已连接"; this.log("WebSocket 已连接"); };
      this.ws.onclose = () => { this.wsStatus = "已断开"; this.log("WebSocket 已断开"); };
      this.ws.onerror = () => { this.wsStatus = "错误"; this.log("WebSocket 错误"); };
      this.ws.onmessage = (evt) => this.handleWsMessage(evt.data);
    },
    handleWsMessage(raw) {
      try {
        const data = JSON.parse(raw);
        if (data.error) return this.log("WS 错误消息: " + data.error);
        const from = data.from;
        const content = data.content;

        if (typeof content === "string") {
          try {
            const sysEvt = JSON.parse(content);
            if (sysEvt?.type === "friend_request_created" || sysEvt?.type === "friend_request_updated") {
              this.log(`收到好友申请事件: ${sysEvt.type}, request_id=${sysEvt.request_id || ""}`);
              this.loadFriendRequestsCenter();
              return;
            }
            if (sysEvt?.type === "group_message" && sysEvt.group_id) {
              const gid = Number(sysEvt.group_id);
              const list = this.groupMessagesMap[gid] || [];
              list.push({
                from_user_id: Number(sysEvt.from_user_id || 0),
                content: String(sysEvt.content || ""),
                seq: Number(sysEvt.seq || 0),
              });
              this.groupMessagesMap[gid] = list;
              this.log(`收到群消息: group=${gid}, from=${sysEvt.from_user_id}, seq=${sysEvt.seq || ""}`);
              if (!this.groupsLocal.find((g) => Number(g.id) === gid)) {
                this.groupsLocal.unshift({ id: gid, name: `群 ${gid}`, owner_user_id: 0 });
                this.persistGroupDrafts();
              }
              return;
            }
          } catch (_) {}
        }

        if (!this.currentPeerId || this.currentPeerId !== from) {
          this.unreadMap[from] = (this.unreadMap[from] || 0) + 1;
          this.loadConversations();
          this.log(`收到来自 ${from} 的消息: ${content}`);
          return;
        }
        this.messages.push({ content, isMe: false });
      } catch (_) {
        this.log("WS 收到非 JSON: " + raw);
      }
    },
    sendPrivateMessage() {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return this.log("WebSocket 尚未连接");
      if (!this.currentPeerId) return this.log("请先选择好友");
      const content = this.msgInput.trim();
      if (!content) return;
      const payload = { to: Number(this.currentPeerId), content, client_msg_id: this.genClientMsgID() };
      this.ws.send(JSON.stringify(payload));
      this.messages.push({ content, isMe: true });
      this.msgInput = "";
      this.loadConversations();
    },

    async sendFriendRequest() {
      const toID = Number(this.friendReqForm.toUserID);
      if (!toID) return this.log("请输入好友 ID");
      try {
        await this.apiRequest("/api/v1/friends/requests", {
          method: "POST",
          body: { to_user_id: toID, remark: "" },
        });
        this.log("好友申请已发送");
        this.friendReqForm.toUserID = "";
        await this.loadFriendRequestsCenter();
      } catch (e) {
        this.log("发送好友申请失败: " + e.message);
      }
    },
    async loadFriendRequestsCenter() {
      try {
        const [inRes, outRes] = await Promise.all([
          this.apiRequest("/api/v1/friends/requests/incoming?status=pending&limit=20"),
          this.apiRequest("/api/v1/friends/requests/outgoing?limit=20"),
        ]);
        this.incomingRequests = inRes.items || [];
        this.outgoingRequests = outRes.items || [];
      } catch (e) {
        this.log("刷新申请中心失败: " + e.message);
      }
    },
    async approveRequest(id) {
      try {
        await this.apiRequest(`/api/v1/friends/requests/${encodeURIComponent(id)}/approve`, { method: "POST" });
        await Promise.all([this.loadFriendRequestsCenter(), this.loadFriends()]);
      } catch (e) {
        this.log("同意申请失败: " + e.message);
      }
    },
    async rejectRequest(id) {
      try {
        await this.apiRequest(`/api/v1/friends/requests/${encodeURIComponent(id)}/reject`, { method: "POST" });
        await this.loadFriendRequestsCenter();
      } catch (e) {
        this.log("拒绝申请失败: " + e.message);
      }
    },

    async createGroup() {
      const name = this.groupForm.name.trim();
      if (!name) return this.log("群名不能为空");
      const ids = [...this.groupCreateMembers];
      try {
        const res = await this.apiRequest("/api/v1/groups", {
          method: "POST",
          body: { name, member_user_ids: ids },
        });
        const group = res.group;
        this.groupsLocal = [group, ...this.groupsLocal.filter((g) => g.id !== group.id)];
        this.persistGroupDrafts();
        this.chatMode = "group";
        this.selectedGroupId = String(group.id);
        this.currentPeerId = null;
        if (!this.groupMessagesMap[Number(group.id)]) this.groupMessagesMap[Number(group.id)] = [];
        this.groupForm = { name: "", memberUserIDs: "" };
        this.groupCreateMembers = [];
        this.groupCreateMemberInput = "";
        this.log(`创建群成功: #${group.id} ${group.name}`);
        await this.loadGroupMembers();
      } catch (e) {
        this.log("创建群失败: " + e.message);
      }
    },
    addCreateMemberChip() {
      const v = Number(this.groupCreateMemberInput);
      if (!Number.isInteger(v) || v <= 0) return;
      if (!this.groupCreateMembers.includes(v)) this.groupCreateMembers.push(v);
      this.groupCreateMemberInput = "";
    },
    removeCreateMemberChip(uid) {
      this.groupCreateMembers = this.groupCreateMembers.filter((x) => x !== uid);
    },
    async loadGroupMembers() {
      const gid = Number(this.selectedGroupId);
      if (!gid) return this.log("请先选择群 ID");
      try {
        const res = await this.apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members`);
        this.groupMembers = res.members || [];
      } catch (e) {
        this.log("加载群成员失败: " + e.message);
      }
    },
    async addGroupMember() {
      const gid = Number(this.selectedGroupId);
      const target = Number(this.groupMemberForm.targetUserID);
      if (!gid || !target) return this.log("请填写群 ID 和目标用户 ID");
      try {
        await this.apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members`, {
          method: "POST",
          body: { target_user_id: target },
        });
        this.groupMemberForm.targetUserID = "";
        await this.loadGroupMembers();
        this.log("添加群成员成功");
      } catch (e) {
        this.log("添加群成员失败: " + e.message);
      }
    },
    async removeGroupMember() {
      const gid = Number(this.selectedGroupId);
      const target = Number(this.groupRemoveUserID);
      if (!gid || !target) return this.log("请填写群 ID 和待移除用户 ID");
      try {
        await this.apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members/${encodeURIComponent(target)}`, {
          method: "DELETE",
        });
        this.groupRemoveUserID = "";
        await this.loadGroupMembers();
        this.log("移除群成员成功");
      } catch (e) {
        this.log("移除群成员失败: " + e.message);
      }
    },
    async sendGroupMessage() {
      const gid = Number(this.selectedGroupId);
      const content = this.groupMsg.trim();
      if (!gid) return this.log("请先选择群 ID");
      if (!content) return;
      try {
        await this.apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/messages`, {
          method: "POST",
          body: { content, client_msg_id: this.genClientMsgID() },
        });
        const list = this.groupMessagesMap[gid] || [];
        list.push({
          from_user_id: this.currentUser ? this.currentUser.id : 0,
          content,
          seq: 0,
        });
        this.groupMessagesMap[gid] = list;
        this.groupMsg = "";
        this.log(`群消息已发送: group=${gid}`);
      } catch (e) {
        this.log("发送群消息失败: " + e.message);
      }
    },
  },
  mounted() {
    this.restoreGroupDrafts();
    this.restoreCollapseState();
    this.pingHealth();
    this.log("Vue 前端已启动");
    this.log("先登录，再用好友申请与群管理功能联调");
  },
  template: `
  <div class="app">
    <header>
      <div>
        <h1>PIM Vue Demo</h1>
        <small>Gateway 统一入口：好友申请中心 + 单聊 + 群管理</small>
      </div>
      <div class="muted">{{ headerUser }}</div>
    </header>
    <main>
      <aside class="sidebar">
        <div class="card col nav-card">
          <div class="section-title">导航 / 操作</div>
          <div class="status-line"><span>HTTP:</span><span>{{ httpStatus }}</span></div>
          <div class="status-line"><span>WS:</span><span>{{ wsStatus }}</span></div>
          <div class="row">
            <button @click="pingHealth">健康检查</button>
            <button @click="connectWs">连接 WS</button>
          </div>
          <div class="row">
            <button @click="togglePanel('auth')">登录/账户</button>
            <button @click="togglePanel('friendReq')">好友申请</button>
            <button @click="togglePanel('group')">群管理</button>
          </div>
          <button @click="logout">退出登录</button>
        </div>

        <div class="card col">
          <div class="section-title">会话列表</div>
          <div class="row">
            <button @click="loadFriends">刷新好友</button>
            <button @click="loadConversations">刷新会话</button>
          </div>
          <div class="list sessions">
            <div
              class="item session-item"
              :class="{active: currentSessionKey === s.key}"
              v-for="s in sessionItems"
              :key="s.key"
              @click="selectSession(s)"
            >
              <div class="session-title">{{ s.title }}</div>
              <div class="session-meta">
                <span class="muted">{{ s.type === 'group' ? '群聊' : '单聊' }}</span>
                <span class="badge" v-if="s.unread">{{ s.unread }}</span>
              </div>
            </div>
            <div class="muted" v-if="sessionItems.length===0">暂无会话，请先添加好友或创建群。</div>
          </div>
        </div>

        <div class="card col" v-if="!collapse.auth">
          <div class="section-title">注册 / 登录</div>
          <div class="row">
            <input v-model="registerForm.username" placeholder="注册用户名" />
            <input v-model="registerForm.password" type="password" placeholder="注册密码" />
          </div>
          <button class="primary" @click="register">注册</button>
          <div class="row">
            <input v-model="loginForm.username" placeholder="登录用户名" />
            <input v-model="loginForm.password" type="password" placeholder="登录密码" />
          </div>
          <button class="primary" @click="login">登录</button>
        </div>

        <div class="card col" v-if="!collapse.friendReq">
          <div class="section-title">好友申请中心</div>
          <div class="row">
            <input v-model="friendReqForm.toUserID" placeholder="对方用户 ID" />
            <button class="primary" @click="sendFriendRequest">发申请</button>
          </div>
          <button @click="loadFriendRequestsCenter">刷新申请中心</button>
          <div class="list">
            <div class="muted">收到的申请</div>
            <div class="item" v-for="r in incomingRequests" :key="'in-' + r.request_id">
              <div>#{{ r.request_id }} from {{ r.from_user_id }} ({{ r.status }})</div>
              <div class="row">
                <button @click="approveRequest(r.request_id)">同意</button>
                <button @click="rejectRequest(r.request_id)">拒绝</button>
              </div>
            </div>
            <div class="muted">我发出的申请</div>
            <div class="item" v-for="r in outgoingRequests" :key="'out-' + r.request_id">
              #{{ r.request_id }} -> {{ r.to_user_id }} ({{ r.status }})
            </div>
          </div>
        </div>

        <div class="card col" v-if="!collapse.group">
          <div class="section-title">群管理</div>
          <div class="row">
            <input v-model="groupForm.name" placeholder="群名" />
            <button class="primary" @click="createGroup">建群</button>
          </div>
          <div class="row">
            <input v-model="groupCreateMemberInput" placeholder="添加初始成员ID" @keydown.enter.prevent="addCreateMemberChip" />
            <button @click="addCreateMemberChip">添加成员</button>
          </div>
          <div class="chip-wrap">
            <span class="chip" v-for="uid in groupCreateMembers" :key="'chip-' + uid">
              {{ uid }}
              <button @click="removeCreateMemberChip(uid)">x</button>
            </span>
          </div>
          <div class="row">
            <input v-model="selectedGroupId" placeholder="当前群 ID" />
            <button @click="loadGroupMembers">查成员</button>
          </div>
          <div class="row">
            <input v-model="groupMemberForm.targetUserID" placeholder="要添加的用户 ID" />
            <button @click="addGroupMember">加成员</button>
          </div>
          <div class="row">
            <input v-model="groupRemoveUserID" placeholder="要移除的用户 ID" />
            <button @click="removeGroupMember">移除成员</button>
          </div>
        </div>
      </aside>

      <section class="content">
        <div class="panel-grid" style="grid-template-columns: 1fr;">
          <div class="card chat-wrap">
            <div class="chat-head">
              <div class="chat-head-main">
                <div class="section-title">当前会话</div>
                <div class="chat-title">{{ currentChatTitle }}</div>
              </div>
              <div class="muted" v-if="chatMode==='group'">群聊</div>
              <div class="muted" v-else>单聊</div>
            </div>

            <div class="muted chat-tip">
              在左侧点击“好友/会话”或“群管理中的群条目”，这里会自动切换会话。
            </div>

            <div class="messages" v-if="chatMode === 'private'">
              <div class="muted" v-if="!currentPeerId">请先在左侧“好友 / 会话”中选择一个好友。</div>
              <div class="muted" v-else-if="messages.length===0">暂无消息，先发一条试试。</div>
              <div class="msg" :class="m.isMe ? 'me' : 'other'" v-for="(m, idx) in messages" :key="'pm-' + idx">{{ m.content }}</div>
            </div>
            <div class="messages" v-else>
              <div class="muted" v-if="!selectedGroupId">请先在左侧“群管理”里选择一个群。</div>
              <div class="muted" v-else-if="(groupMessagesMap[Number(selectedGroupId)] || []).length===0">当前群暂无消息。</div>
              <div class="group-msg-line" v-for="(gm, idx) in (groupMessagesMap[Number(selectedGroupId)] || [])" :key="'gm-main-' + idx">
                <div>from {{ gm.from_user_id }} <span v-if="gm.seq">#{{ gm.seq }}</span></div>
                <div>{{ gm.content }}</div>
              </div>
            </div>

            <div class="composer" v-if="chatMode === 'private'">
              <input v-model="msgInput" placeholder="输入单聊消息" @keydown.enter.prevent="sendPrivateMessage" />
              <button class="primary" :disabled="!canSendPrivate" @click="sendPrivateMessage">发送</button>
            </div>
            <div class="composer" v-else>
              <input v-model="groupMsg" placeholder="输入群消息" @keydown.enter.prevent="sendGroupMessage" />
              <button class="primary" :disabled="!canSendGroup" @click="sendGroupMessage">发送</button>
            </div>
          </div>
        </div>
        <div class="logs">{{ debugLogs.join('\\n') }}</div>
      </section>
    </main>
  </div>
  `,
}).mount("#app");

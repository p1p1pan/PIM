const { createApp } = Vue;

createApp({
  data() {
    const auth = loadAuth();
    return {
      token: auth.token || "",
      currentUser: auth.user || null,
      ws: null,
      wsStatus: "未连接",
      leftTab: "sessions",
      contactsTab: "friends",
      showDebug: false,
      showPlusMenu: false,
      searchText: "",
      friends: [],
      conversations: [],
      unreadMap: {},
      groupUnreadMap: {},
      currentPeerId: null,
      selectedGroupId: "",
      messages: [],
      groupMessagesMap: {},
      groupHistoryState: {},
      groupDetailsMap: {},
      groupMembersMap: {},
      msgInput: "",
      groupMsg: "",
      incomingRequests: [],
      outgoingRequests: [],
      groupsLocal: [],
      showCreateGroupModal: false,
      createGroupName: "",
      createGroupPickFilter: "",
      selectedFriendIDs: [],
      showAddFriendModal: false,
      friendRequestIDInput: "",
      showMyProfileModal: false,
      editNicknameInput: "",
      autoSyncTimer: null,
      pendingSyncTimer: null,
      logs: [],
      showGroupPanel: false,
      groupMemberAddInput: "",
      groupTransferInput: "",
      fileMetaMap: {},
      sendingFile: false,
    };
  },
  computed: {
    userInitial() {
      const name = this.selfDisplayName || "U";
      return String(name).slice(0, 1).toUpperCase();
    },
    selfDisplayName() {
      const nick = String(this.currentUser?.nickname || "").trim();
      const user = String(this.currentUser?.username || "").trim();
      if (nick) return nick;
      if (user) return user;
      return "未登录";
    },
    sessionItems() {
      const map = new Map();
      for (const c of this.conversations || []) {
        const peerID = Number(c.peer_id || 0);
        if (!peerID) continue;
        map.set(`private:${peerID}`, {
          key: `private:${peerID}`,
          type: "private",
          id: peerID,
          title: this.peerLabel(peerID),
          desc: String(c.last_preview || "").trim() || "单聊",
          unread: Number(this.unreadMap[peerID] || 0),
          ts: Number(c.updated_at || c.last_message_at || c.id || 0),
        });
      }
      for (const f of this.friends || []) {
        const peerID = Number(f.friend_id || 0);
        if (!peerID) continue;
        const key = `private:${peerID}`;
        const title = this.friendDisplayName(f);
        if (!map.has(key)) {
          map.set(key, {
            key,
            type: "private",
            id: peerID,
            title,
            desc: "单聊",
            unread: Number(this.unreadMap[peerID] || 0),
            ts: 0,
          });
        } else {
          map.get(key).title = title;
        }
      }
      const groups = (this.groupsLocal || []).map((g) => ({
        key: `group:${Number(g.id)}`,
        type: "group",
        id: Number(g.id),
        title: g.name || `群 #${g.id}`,
        desc: "群聊",
        unread: Number(this.groupUnreadMap[Number(g.id)] || 0),
        ts: Number(g.updated_at || g.created_at || g.id || 0),
      }));
      return [...map.values(), ...groups].sort((a, b) => b.ts - a.ts);
    },
    filteredSessionItems() {
      const q = this.searchText.trim().toLowerCase();
      return this.sessionItems.filter((x) => x.title.toLowerCase().includes(q));
    },
    filteredFriends() {
      const q = this.searchText.trim().toLowerCase();
      return (this.friends || []).filter((f) => this.friendDisplayName(f).toLowerCase().includes(q));
    },
    incomingPendingCount() {
      return (this.incomingRequests || []).filter((r) => String(r.status || "").toLowerCase() === "pending" || !r.status).length;
    },
    filteredIncomingRequests() {
      const q = this.searchText.trim().toLowerCase();
      return (this.incomingRequests || []).filter((r) => {
        const text = `${this.requestPeerName(r, "from")} ${r.status || ""}`.toLowerCase();
        return text.includes(q);
      });
    },
    filteredOutgoingRequests() {
      const q = this.searchText.trim().toLowerCase();
      return (this.outgoingRequests || []).filter((r) => {
        const text = `${this.requestPeerName(r, "to")} ${this.friendRequestStatusLabel(r.status)}`.toLowerCase();
        return text.includes(q);
      });
    },
    wsBannerText() {
      const s = String(this.wsStatus || "");
      if (s === "已连接") return "";
      if (s.includes("重连") || s.includes("断开") || s === "连接中" || s === "未连接") {
        return s === "连接中" ? "正在连接消息通道…" : `消息通道：${s}（收发可能暂时不可用）`;
      }
      return s ? `消息通道：${s}` : "";
    },
    wsBannerClass() {
      const s = String(this.wsStatus || "");
      if (s === "连接中") return "ws-warn";
      return "ws-error";
    },
    currentSessionKey() {
      if (this.selectedGroupId) return `group:${Number(this.selectedGroupId)}`;
      if (this.currentPeerId) return `private:${Number(this.currentPeerId)}`;
      return "";
    },
    currentTitle() {
      if (this.selectedGroupId) {
        const gid = Number(this.selectedGroupId);
        const g = this.groupsLocal.find((x) => Number(x.id) === gid);
        return g ? `${g.name} (#${g.id})` : `群 #${gid}`;
      }
      if (this.currentPeerId) return this.peerLabel(this.currentPeerId);
      return "请选择会话";
    },
    currentChatMessages() {
      if (this.selectedGroupId) return this.groupMessagesMap[Number(this.selectedGroupId)] || [];
      return this.messages || [];
    },
    canSendPrivate() {
      return !!this.currentPeerId && !!this.msgInput.trim() && this.ws?.readyState === WebSocket.OPEN;
    },
    canSendGroup() {
      return !!this.selectedGroupId && !!this.groupMsg.trim();
    },
    currentGroupID() {
      return Number(this.selectedGroupId || 0);
    },
    currentGroupDetail() {
      if (!this.currentGroupID) return null;
      return this.groupDetailsMap[this.currentGroupID] || null;
    },
    currentGroupMembers() {
      if (!this.currentGroupID) return [];
      return this.groupMembersMap[this.currentGroupID] || [];
    },
    isGroupOwner() {
      const d = this.currentGroupDetail;
      return !!d && Number(d.owner_user_id || 0) === Number(this.currentUser?.id || 0);
    },
    groupInitial() {
      const name = String(this.currentGroupDetail?.name || "群").trim();
      return name ? name.charAt(0) : "群";
    },
    filteredFriendsForCreateGroup() {
      const q = String(this.createGroupPickFilter || "").trim().toLowerCase();
      const list = this.friends || [];
      if (!q) return list;
      return list.filter((f) => this.friendDisplayName(f).toLowerCase().includes(q));
    },
    selectedFriendsForCreateGroup() {
      const ids = new Set((this.selectedFriendIDs || []).map((x) => Number(x)));
      return (this.friends || []).filter((f) => ids.has(Number(f.friend_id)));
    },
    canConfirmCreateGroup() {
      return (this.friends || []).length > 0 && (this.selectedFriendIDs || []).length > 0;
    },
  },
  methods: Object.assign(window.buildIMMethods(), {
    isFriendPicked(friendId) {
      const id = Number(friendId);
      return (this.selectedFriendIDs || []).some((x) => Number(x) === id);
    },
    toggleFriendPick(friendId) {
      const id = Number(friendId);
      if (!id) return;
      const list = (this.selectedFriendIDs || []).map((x) => Number(x));
      const idx = list.indexOf(id);
      if (idx >= 0) list.splice(idx, 1);
      else list.push(id);
      this.selectedFriendIDs = list;
    },
    friendInitial(f) {
      const name = this.friendDisplayName(f);
      return name ? String(name).trim().charAt(0) : "?";
    },
    peerInitial(userId) {
      const label = this.peerLabel(userId);
      return label ? String(label).trim().charAt(0) : "?";
    },
    messageAvatarInitial(m) {
      if (!m) return "?";
      if (m.isMe) return this.userInitial || "我";
      return this.peerInitial(m.from_user_id);
    },
    memberDisplayName(m) {
      if (!m) return "未知用户";
      const nick = String(m.nickname || "").trim();
      const user = String(m.username || "").trim();
      if (nick) return nick;
      if (user) return user;
      const id = Number(m.user_id || 0);
      if (id && Number(this.currentUser?.id || 0) === id) {
        const selfNick = String(this.currentUser?.nickname || "").trim();
        const selfUser = String(this.currentUser?.username || "").trim();
        if (selfNick) return selfNick;
        if (selfUser) return selfUser;
      }
      const f = (this.friends || []).find((x) => Number(x.friend_id) === id);
      if (f) return this.friendDisplayName(f);
      return id ? `用户 #${id}` : "未知用户";
    },
    memberInitial(m) {
      const name = this.memberDisplayName(m);
      return name ? String(name).trim().charAt(0) : "?";
    },
    memberSecondaryLine(m) {
      if (!m) return "";
      const id = Number(m.user_id || 0);
      const nick = String(m.nickname || "").trim();
      const user = String(m.username || "").trim();
      // 有昵称时副行展示账号；否则只展示 ID，避免和主行「用户 #id」重复。
      if (nick && user) return `@${user}`;
      if (id) return `ID ${id}`;
      return "";
    },
    groupRoleLabel(role) {
      const r = String(role || "").toLowerCase();
      if (r === "owner") return "群主";
      if (r === "admin") return "管理员";
      if (r === "member") return "成员";
      return role ? String(role) : "成员";
    },
    openMyProfileModal() {
      this.editNicknameInput = String(this.currentUser?.nickname || "");
      this.showMyProfileModal = true;
    },
    async copyText(text) {
      const v = String(text || "").trim();
      if (!v) throw new Error("没有可复制内容");
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(v);
      } else {
        const ta = document.createElement("textarea");
        ta.value = v;
        document.body.appendChild(ta);
        ta.select();
        document.execCommand("copy");
        document.body.removeChild(ta);
      }
      this.log("已复制");
    },
    async copyMyUserId() {
      const id = String(this.currentUser?.id || "").trim();
      if (!id) throw new Error("没有用户 ID");
      await this.copyText(id);
    },
    friendDisplayName(f) {
      if (!f) return "未知用户";
      const nick = String(f.nickname || "").trim();
      const user = String(f.username || "").trim();
      if (nick) return nick;
      if (user) return user;
      const id = Number(f.friend_id || 0);
      return id ? `用户 #${id}` : "未知用户";
    },
    requestPeerName(r, side) {
      if (!r) return "未知用户";
      const nick = String(r.nickname || "").trim();
      const user = String(r.username || "").trim();
      if (nick) return nick;
      if (user) return user;
      const id = Number(side === "to" ? r.to_user_id : r.from_user_id) || 0;
      return id ? `用户 #${id}` : "未知用户";
    },
    peerLabel(userId) {
      const id = Number(userId || 0);
      if (!id) return "未知用户";
      if (Number(this.currentUser?.id || 0) === id) {
        const selfNick = String(this.currentUser?.nickname || "").trim();
        const selfUser = String(this.currentUser?.username || "").trim();
        if (selfNick) return selfNick;
        if (selfUser) return selfUser;
      }
      const member = (this.currentGroupMembers || []).find((x) => Number(x.user_id) === id);
      if (member) {
        const nick = String(member.nickname || "").trim();
        const user = String(member.username || "").trim();
        if (nick) return nick;
        if (user) return user;
      }
      const f = (this.friends || []).find((x) => Number(x.friend_id) === id);
      if (f) return this.friendDisplayName(f);
      return `用户 #${id}`;
    },
  }),
  mounted() {
    this.safeCall(() => this.bootstrap(), "初始化失败");
  },
  beforeUnmount() {
    this.stopAutoSync();
  },
  template: window.IM_TEMPLATE,
}).mount("#app");

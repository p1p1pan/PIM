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
      selectedFriendIDs: [],
      showAddFriendModal: false,
      friendRequestIDInput: "",
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
      return String(this.currentUser?.username || "U").slice(0, 1).toUpperCase();
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
          title: `用户 #${peerID}`,
          desc: "单聊",
          unread: Number(this.unreadMap[peerID] || 0),
          ts: Number(c.updated_at || c.last_message_at || c.id || 0),
        });
      }
      for (const f of this.friends || []) {
        const peerID = Number(f.friend_id || 0);
        if (!peerID) continue;
        const key = `private:${peerID}`;
        if (!map.has(key)) {
          map.set(key, {
            key,
            type: "private",
            id: peerID,
            title: `用户 #${peerID}`,
            desc: "单聊",
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
      return (this.friends || []).filter((f) => `用户 #${f.friend_id}`.toLowerCase().includes(q));
    },
    filteredIncomingRequests() {
      const q = this.searchText.trim().toLowerCase();
      return (this.incomingRequests || []).filter((r) => {
        const text = `#${r.from_user_id} ${r.status || ""}`.toLowerCase();
        return text.includes(q);
      });
    },
    filteredOutgoingRequests() {
      const q = this.searchText.trim().toLowerCase();
      return (this.outgoingRequests || []).filter((r) => {
        const text = `#${r.to_user_id} ${r.status || ""}`.toLowerCase();
        return text.includes(q);
      });
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
      if (this.currentPeerId) return `用户 #${this.currentPeerId}`;
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
  },
  methods: window.buildIMMethods(),
  mounted() {
    this.safeCall(() => this.bootstrap(), "初始化失败");
  },
  beforeUnmount() {
    this.stopAutoSync();
  },
  template: window.IM_TEMPLATE,
}).mount("#app");

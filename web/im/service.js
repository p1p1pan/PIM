window.buildIMMethods = function buildIMMethods() {
  return {
    parseMessagePayload(content) {
      const text = String(content || "");
      try {
        const obj = JSON.parse(text);
        if (obj && obj.type === "file" && Number(obj.file_id || 0) > 0) {
          return {
            uiType: "file",
            fileID: Number(obj.file_id),
            fileName: String(obj.file_name || `文件 #${obj.file_id}`),
            text,
          };
        }
      } catch (_) {}
      return { uiType: "text", text };
    },
    async ensureFileMeta(fileID) {
      const id = Number(fileID || 0);
      if (!id) return null;
      if (this.fileMetaMap[id]) return this.fileMetaMap[id];
      const res = await apiRequest(`/api/v1/files/${encodeURIComponent(id)}`);
      this.fileMetaMap[id] = res.file || null;
      return this.fileMetaMap[id];
    },
    async sendFileMessageFromInput(evt) {
      const files = evt?.target?.files;
      if (!files || !files.length) return;
      const file = files[0];
      if (!file) return;
      this.sendingFile = true;
      try {
        const isGroup = !!this.selectedGroupId;
        const prepare = await apiRequest("/api/v1/files/prepare", {
          method: "POST",
          body: {
            client_upload_id: `upl_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`,
            file_name: file.name || "unnamed-file",
            file_size: Number(file.size || 0),
            mime_type: file.type || "application/octet-stream",
            biz_type: isGroup ? "group" : "private",
            peer_id: isGroup ? 0 : Number(this.currentPeerId || 0),
            group_id: isGroup ? Number(this.selectedGroupId || 0) : 0,
          },
        });
        const f = prepare.file || {};
        if (!f.upload_url) throw new Error("上传地址缺失");
        const uploadResp = await fetch(String(f.upload_url), {
          method: "PUT",
          body: file,
        });
        if (!uploadResp.ok) {
          const t = await uploadResp.text();
          throw new Error(t || `文件上传失败: HTTP ${uploadResp.status}`);
        }
        await apiRequest(`/api/v1/files/${encodeURIComponent(f.id)}/commit`, { method: "POST", body: {} });
        await this.ensureFileMeta(f.id);
        const payload = JSON.stringify({ type: "file", file_id: Number(f.id), file_name: String(f.file_name || file.name || "") });
        if (isGroup) {
          this.groupMsg = payload;
          await this.sendGroupMessage();
        } else {
          this.msgInput = payload;
          await this.sendPrivateMessage();
        }
      } finally {
        this.sendingFile = false;
        if (evt?.target) evt.target.value = "";
      }
    },
    mergeIncomingGroupMessage(gid, incoming) {
      const list = this.groupMessagesMap[gid] || [];
      const seq = Number(incoming.seq || 0);
      const fromID = Number(incoming.from_user_id || 0);
      const content = String(incoming.content || "");
      if (seq > 0) {
        const idx = list.findIndex((m) => Number(m.seq || 0) === seq);
        if (idx >= 0) {
          list[idx] = { ...list[idx], ...incoming, localEcho: false };
          this.groupMessagesMap[gid] = list;
          return;
        }
      }
      const echoIdx = list.findIndex((m) =>
        m.localEcho &&
        Number(m.from_user_id || 0) === fromID &&
        String(m.content || "") === content
      );
      if (echoIdx >= 0) {
        list[echoIdx] = { ...list[echoIdx], ...incoming, localEcho: false };
        this.groupMessagesMap[gid] = list;
        return;
      }
      list.push({ ...incoming, localEcho: false });
      this.groupMessagesMap[gid] = list;
    },
    log(msg) {
      const t = new Date().toLocaleTimeString();
      this.logs.push(`[${t}] ${msg}`);
      if (this.logs.length > 120) this.logs.shift();
    },
    async safeCall(fn, fallbackText) {
      try {
        await fn();
      } catch (e) {
        this.log(`${fallbackText}: ${e.message}`);
      }
    },
    logout() {
      this.stopAutoSync();
      clearAuth();
      if (this.ws) {
        try { this.ws.close(); } catch (_) {}
      }
      location.replace("./login.html");
    },
    connectWs() {
      if (this.ws?.readyState === WebSocket.OPEN) return;
      this.ws = new WebSocket(`${WS_BASE}/ws?token=${encodeURIComponent(this.token)}`);
      this.ws.onopen = () => {
        this.wsStatus = "已连接";
        this.log("WebSocket 已连接");
      };
      this.ws.onclose = () => {
        this.wsStatus = "已断开";
        this.log("WebSocket 已断开");
      };
      this.ws.onerror = () => {
        this.wsStatus = "错误";
        this.log("WebSocket 连接错误");
      };
      this.ws.onmessage = (evt) => this.handleWsMessage(evt.data);
    },
    handleWsMessage(raw) {
      try {
        const data = JSON.parse(raw);
        if (data.error) return;
        const from = Number(data.from || 0);
        const content = String(data.content || "");
        if (content) {
          try {
            const evt = JSON.parse(content);
            if (evt?.type === "friend_request_created" || evt?.type === "friend_request_updated") {
              this.queueLightSync();
              return;
            }
            if (typeof evt?.type === "string" && evt.type.startsWith("group_")) {
              const gid = Number(evt.group_id || 0);
              if (evt.type === "group_disbanded" && gid) {
                this.groupsLocal = (this.groupsLocal || []).filter((g) => Number(g.id) !== gid);
                delete this.groupMessagesMap[gid];
                delete this.groupDetailsMap[gid];
                delete this.groupMembersMap[gid];
                if (Number(this.selectedGroupId || 0) === gid) {
                  this.selectedGroupId = "";
                  this.showGroupPanel = false;
                }
                this.queueLightSync();
                return;
              }
              if ((evt.type === "group_member_removed" || evt.type === "group_member_left") && gid) {
                const target = Number(evt.target_user_id || evt.user_id || 0);
                if (target === Number(this.currentUser?.id || 0)) {
                  this.groupsLocal = (this.groupsLocal || []).filter((g) => Number(g.id) !== gid);
                  delete this.groupMessagesMap[gid];
                  delete this.groupDetailsMap[gid];
                  delete this.groupMembersMap[gid];
                  if (Number(this.selectedGroupId || 0) === gid) this.selectedGroupId = "";
                  if (Number(this.currentGroupID || 0) === gid) this.showGroupPanel = false;
                }
                this.queueLightSync();
                return;
              }
              if ((evt.type === "group_created" || evt.type === "group_updated" || evt.type === "group_member_added" || evt.type === "group_owner_transferred") && gid) {
                const idx = (this.groupsLocal || []).findIndex((g) => Number(g.id) === gid);
                const name = String(evt.name || `群聊 #${gid}`);
                if (idx >= 0) {
                  this.groupsLocal[idx] = {
                    ...this.groupsLocal[idx],
                    name,
                    owner_user_id: Number(evt.owner_user_id || evt.new_owner_user_id || this.groupsLocal[idx].owner_user_id || 0),
                    notice: String(evt.notice || this.groupsLocal[idx].notice || ""),
                  };
                } else {
                  this.groupsLocal.unshift({
                    id: gid,
                    name,
                    owner_user_id: Number(evt.owner_user_id || evt.new_owner_user_id || 0),
                    notice: String(evt.notice || ""),
                  });
                }
                this.queueLightSync();
                return;
              }
            }
            if (evt?.group_id && evt?.content) {
              const gid = Number(evt.group_id);
              if (gid) {
                if (!this.groupsLocal.find((g) => Number(g.id) === gid)) {
                  this.groupsLocal.unshift({
                    id: gid,
                    name: String(evt.group_name || `群聊 #${gid}`),
                  });
                }
                const senderID = Number(evt.from_user_id || from || 0);
                const payload = this.parseMessagePayload(String(evt.content || ""));
                this.mergeIncomingGroupMessage(gid, {
                  from_user_id: senderID,
                  message_type: String(evt.message_type || "text"),
                  content: payload.text,
                  ui_type: payload.uiType,
                  file_id: payload.fileID,
                  file_name: payload.fileName,
                  seq: Number(evt.seq || 0),
                  isMe: senderID === Number(this.currentUser?.id || 0),
                });
                if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载文件元信息失败");
                if (Number(this.selectedGroupId || 0) !== gid) {
                  this.groupUnreadMap[gid] = Number(this.groupUnreadMap[gid] || 0) + 1;
                } else {
                  this.safeCall(() => this.markCurrentGroupRead(), "更新群已读失败");
                }
                this.queueLightSync();
                return;
              }
            }
          } catch (_) {}
        }
        if (!from || !content) return;
        if (!this.selectedGroupId && this.currentPeerId === from) {
          const payload = this.parseMessagePayload(content);
          const item = { from_user_id: from, content: payload.text, isMe: false, ui_type: payload.uiType, file_id: payload.fileID, file_name: payload.fileName };
          this.messages.push(item);
          if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载文件元信息失败");
          this.queueLightSync();
          return;
        }
        this.unreadMap[from] = Number(this.unreadMap[from] || 0) + 1;
        this.queueLightSync();
      } catch (_) {}
    },
    async loadFriends() {
      const res = await apiRequest("/api/v1/friends");
      this.friends = res.friends || [];
    },
    async loadConversations() {
      const res = await apiRequest("/api/v1/conversations");
      this.conversations = res.conversations || [];
    },
    async loadFriendRequestsCenter() {
      const [inRes, outRes] = await Promise.all([
        apiRequest("/api/v1/friends/requests/incoming?status=pending&limit=20"),
        apiRequest("/api/v1/friends/requests/outgoing?limit=20"),
      ]);
      this.incomingRequests = inRes.items || [];
      this.outgoingRequests = outRes.items || [];
    },
    async loadMyGroups() {
      const [res, convRes] = await Promise.all([
        apiRequest("/api/v1/groups"),
        apiRequest("/api/v1/groups/conversations"),
      ]);
      const groups = res.groups || [];
      this.groupsLocal = groups;
      const unreadMap = {};
      for (const it of convRes.items || []) {
        const g = it.group || {};
        const gid = Number(g.id || 0);
        if (!gid) continue;
        unreadMap[gid] = Number(it.unread_count || 0);
      }
      this.groupUnreadMap = unreadMap;
      for (const g of groups) {
        const gid = Number(g.id);
        if (gid && !this.groupMessagesMap[gid]) this.groupMessagesMap[gid] = [];
      }
    },
    async refreshAll() {
      await Promise.all([
        this.loadFriends(),
        this.loadConversations(),
        this.loadFriendRequestsCenter(),
        this.loadMyGroups(),
      ]);
    },
    async openPrivateById(friendId) {
      await this.selectSession({ type: "private", id: Number(friendId) });
      this.leftTab = "sessions";
    },
    async selectSession(item) {
      if (item.type === "group") {
        this.selectedGroupId = String(item.id);
        this.groupUnreadMap[Number(item.id)] = 0;
        this.currentPeerId = null;
        await this.loadGroupMessages(item.id, { reset: true });
        await this.loadGroupDetail(item.id);
        await this.loadGroupMembers(item.id);
        await this.markCurrentGroupRead();
        return;
      }
      this.selectedGroupId = "";
      this.showGroupPanel = false;
      this.currentPeerId = Number(item.id);
      this.unreadMap[item.id] = 0;
      const res = await apiRequest(`/api/v1/messages?with=${encodeURIComponent(item.id)}`);
      this.messages = (res.messages || []).map((m) => ({
        ...(() => {
          const payload = this.parseMessagePayload(m.content);
          if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载历史文件元信息失败");
          return {
            from_user_id: m.from_user_id,
            content: payload.text,
            ui_type: payload.uiType,
            file_id: payload.fileID,
            file_name: payload.fileName,
            isMe: this.currentUser?.id === m.from_user_id,
          };
        })(),
      }));
      try {
        await apiRequest(`/api/v1/conversations/${encodeURIComponent(item.id)}/read`, { method: "PUT" });
      } catch (_) {}
    },
    async sendPrivateMessage() {
      const content = this.msgInput.trim();
      if (!content || !this.currentPeerId || this.ws?.readyState !== WebSocket.OPEN) return;
      this.ws.send(JSON.stringify({
        to: Number(this.currentPeerId),
        content,
        client_msg_id: `c_${Date.now()}`,
      }));
      const payload = this.parseMessagePayload(content);
      this.messages.push({
        from_user_id: this.currentUser?.id || 0,
        content: payload.text,
        ui_type: payload.uiType,
        file_id: payload.fileID,
        file_name: payload.fileName,
        isMe: true,
      });
      if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "发送后刷新文件元信息失败");
      this.msgInput = "";
      await this.loadConversations();
    },
    async sendGroupMessage() {
      const gid = Number(this.selectedGroupId);
      const content = this.groupMsg.trim();
      if (!gid || !content) return;
      const clientMsgID = `g_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 10)}`;
      const myID = Number(this.currentUser?.id || 0);
      const payload = this.parseMessagePayload(content);
      const list = this.groupMessagesMap[gid] || [];
      list.push({
        from_user_id: myID,
        content: payload.text,
        ...(payload.uiType === "file"
          ? {
              ui_type: "file",
              file_id: payload.fileID,
              file_name: payload.fileName,
            }
          : {}),
        seq: 0,
        isMe: true,
        localEcho: true,
      });
      if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "发送后刷新文件元信息失败");
      this.groupMessagesMap[gid] = list;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/messages`, {
        method: "POST",
        body: { content, client_msg_id: clientMsgID },
      });
      this.groupMsg = "";
    },
    async loadGroupMessages(groupID, opts = {}) {
      const gid = Number(groupID);
      if (!gid) return;
      const reset = !!opts.reset;
      const beforeSeq = Number(opts.beforeSeq || 0);
      const qs = new URLSearchParams();
      qs.set("limit", String(opts.limit || 30));
      if (beforeSeq > 0) qs.set("before_seq", String(beforeSeq));
      const res = await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/messages?${qs.toString()}`);
      const messages = (res.messages || []).map((m) => ({
        ...(() => {
          const payload = this.parseMessagePayload(m.content);
          if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载历史文件元信息失败");
          return {
            from_user_id: Number(m.from_user_id || 0),
            message_type: String(m.message_type || "text"),
            content: payload.text,
            ui_type: payload.uiType,
            file_id: payload.fileID,
            file_name: payload.fileName,
            seq: Number(m.seq || 0),
            isMe: Number(m.from_user_id || 0) === Number(this.currentUser?.id || 0),
          };
        })(),
      }));
      if (reset) {
        this.groupMessagesMap[gid] = messages;
      } else {
        const current = this.groupMessagesMap[gid] || [];
        const merged = [...messages, ...current];
        const seen = new Set();
        this.groupMessagesMap[gid] = merged.filter((m) => {
          const key = `${m.seq}:${m.from_user_id}:${m.content}`;
          if (seen.has(key)) return false;
          seen.add(key);
          return true;
        });
      }
      this.groupHistoryState[gid] = {
        hasMore: !!res.has_more,
        nextBeforeSeq: Number(res.next_before_seq || 0),
        loading: false,
      };
    },
    async loadGroupDetail(groupID) {
      const gid = Number(groupID);
      if (!gid) return;
      const res = await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}`);
      if (res.group) {
        this.groupDetailsMap[gid] = res.group;
      }
    },
    async loadGroupMembers(groupID) {
      const gid = Number(groupID);
      if (!gid) return;
      const res = await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members`);
      this.groupMembersMap[gid] = res.members || [];
    },
    async openGroupPanel() {
      if (!this.currentGroupID) return;
      this.showGroupPanel = true;
      await Promise.all([
        this.loadGroupDetail(this.currentGroupID),
        this.loadGroupMembers(this.currentGroupID),
      ]);
    },
    closeGroupPanel() {
      this.showGroupPanel = false;
      this.groupMemberAddInput = "";
      this.groupTransferInput = "";
    },
    async saveGroupProfile() {
      if (!this.currentGroupID || !this.currentGroupDetail) return;
      const name = String(this.currentGroupDetail.name || "").trim();
      const notice = String(this.currentGroupDetail.notice || "").trim();
      if (!name) throw new Error("群名称不能为空");
      const res = await apiRequest(`/api/v1/groups/${encodeURIComponent(this.currentGroupID)}`, {
        method: "PUT",
        body: { name, notice },
      });
      this.groupDetailsMap[this.currentGroupID] = res.group || this.currentGroupDetail;
      await this.refreshAll();
    },
    async addGroupMemberByID() {
      const gid = this.currentGroupID;
      const uid = Number(this.groupMemberAddInput || 0);
      if (!gid || !uid) throw new Error("群 ID 或用户 ID 无效");
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members`, {
        method: "POST",
        body: { target_user_id: uid },
      });
      this.groupMemberAddInput = "";
      await this.loadGroupMembers(gid);
      await this.refreshAll();
    },
    async removeGroupMember(userID) {
      const gid = this.currentGroupID;
      const uid = Number(userID || 0);
      if (!gid || !uid) return;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members/${encodeURIComponent(uid)}`, {
        method: "DELETE",
      });
      await this.loadGroupMembers(gid);
      await this.refreshAll();
    },
    async transferGroupOwnerByID() {
      const gid = this.currentGroupID;
      const uid = Number(this.groupTransferInput || 0);
      if (!gid || !uid) throw new Error("群 ID 或用户 ID 无效");
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/transfer-owner`, {
        method: "POST",
        body: { target_user_id: uid },
      });
      this.groupTransferInput = "";
      await Promise.all([this.loadGroupDetail(gid), this.loadGroupMembers(gid), this.refreshAll()]);
    },
    async leaveCurrentGroup() {
      const gid = this.currentGroupID;
      if (!gid) return;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/leave`, { method: "POST" });
      this.closeGroupPanel();
      this.selectedGroupId = "";
      delete this.groupMessagesMap[gid];
      await this.refreshAll();
    },
    async disbandCurrentGroup() {
      const gid = this.currentGroupID;
      if (!gid) return;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/disband`, { method: "POST" });
      this.closeGroupPanel();
      this.selectedGroupId = "";
      delete this.groupMessagesMap[gid];
      await this.refreshAll();
    },
    async markCurrentGroupRead() {
      const gid = Number(this.selectedGroupId);
      if (!gid) return;
      const list = this.groupMessagesMap[gid] || [];
      let maxSeq = 0;
      for (const m of list) {
        const seq = Number(m.seq || 0);
        if (seq > maxSeq) maxSeq = seq;
      }
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/read`, {
        method: "PUT",
        body: { read_seq: maxSeq },
      });
      this.groupUnreadMap[gid] = 0;
    },
    async loadMoreGroupMessagesIfNeeded() {
      const gid = Number(this.selectedGroupId);
      if (!gid) return;
      const st = this.groupHistoryState[gid] || {};
      if (st.loading || !st.hasMore || !st.nextBeforeSeq) return;
      st.loading = true;
      this.groupHistoryState[gid] = st;
      const box = this.$refs.chatMessages;
      const oldHeight = box ? box.scrollHeight : 0;
      await this.loadGroupMessages(gid, { beforeSeq: st.nextBeforeSeq, limit: 30, reset: false });
      this.$nextTick(() => {
        if (!box) return;
        const newHeight = box.scrollHeight;
        box.scrollTop = newHeight - oldHeight + box.scrollTop;
      });
    },
    async onChatScroll(evt) {
      if (!this.selectedGroupId) return;
      const el = evt?.target;
      if (!el) return;
      if (el.scrollTop <= 40) {
        await this.safeCall(() => this.loadMoreGroupMessagesIfNeeded(), "加载更多群消息失败");
      }
    },
    openCreateGroupModal() {
      this.createGroupName = "";
      this.selectedFriendIDs = [];
      this.showCreateGroupModal = true;
    },
    async confirmCreateGroupBySelection() {
      const name = this.createGroupName.trim() || `新群聊 ${Date.now().toString().slice(-4)}`;
      const members = this.selectedFriendIDs.map((x) => Number(x)).filter((x) => Number.isInteger(x) && x > 0);
      const res = await apiRequest("/api/v1/groups", {
        method: "POST",
        body: { name, member_user_ids: members },
      });
      const group = res.group;
      this.groupsLocal = [group, ...this.groupsLocal.filter((g) => Number(g.id) !== Number(group.id))];
      this.showCreateGroupModal = false;
      await this.selectSession({ type: "group", id: group.id });
      await this.loadConversations();
    },
    openAddFriendModal() {
      this.friendRequestIDInput = "";
      this.showAddFriendModal = true;
    },
    async confirmSendFriendRequestByID() {
      const toID = Number(this.friendRequestIDInput);
      if (!toID) return;
      await apiRequest("/api/v1/friends/requests", {
        method: "POST",
        body: { to_user_id: toID, remark: "" },
      });
      this.showAddFriendModal = false;
      await this.loadFriendRequestsCenter();
      this.log(`已发送好友申请 #${toID}`);
    },
    async approveRequest(id) {
      await apiRequest(`/api/v1/friends/requests/${encodeURIComponent(id)}/approve`, { method: "POST" });
      await Promise.all([this.loadFriendRequestsCenter(), this.loadFriends()]);
    },
    async rejectRequest(id) {
      await apiRequest(`/api/v1/friends/requests/${encodeURIComponent(id)}/reject`, { method: "POST" });
      await this.loadFriendRequestsCenter();
    },
    async deleteFriendByID(userID) {
      const uid = Number(userID || 0);
      if (!uid) throw new Error("用户 ID 无效");
      if (!window.confirm(`确认删除好友 #${uid} 吗？`)) return;
      await apiRequest(`/api/v1/friends/${encodeURIComponent(uid)}`, { method: "DELETE" });
      await Promise.all([
        this.loadFriends(),
        this.loadFriendRequestsCenter(),
      ]);
      // 如果当前正在和该好友聊天，删除后清空会话区，避免误发消息。
      if (Number(this.currentPeerId || 0) === uid) {
        this.currentPeerId = null;
        this.messages = [];
      }
      this.log(`已删除好友 #${uid}`);
    },
    async bootstrap() {
      if (!this.token || !this.currentUser) {
        location.replace("./login.html");
        return;
      }
      this.connectWs();
      await this.refreshAll();
      this.startAutoSync();
    },
    startAutoSync() {
      this.stopAutoSync();
      this.autoSyncTimer = setInterval(() => {
        if (document.hidden) return;
        this.safeCall(() => this.refreshAll(), "自动刷新失败");
      }, 3000);
    },
    stopAutoSync() {
      if (this.autoSyncTimer) {
        clearInterval(this.autoSyncTimer);
        this.autoSyncTimer = null;
      }
      if (this.pendingSyncTimer) {
        clearTimeout(this.pendingSyncTimer);
        this.pendingSyncTimer = null;
      }
    },
    queueLightSync() {
      if (this.pendingSyncTimer) return;
      this.pendingSyncTimer = setTimeout(async () => {
        this.pendingSyncTimer = null;
        await this.safeCall(() => this.refreshAll(), "轻量刷新失败");
      }, 400);
    },
  };
};


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
      this.wsStatus = "连接中";
      this.ws = new WebSocket(`${WS_BASE}/ws?token=${encodeURIComponent(this.token)}`);
      this.ws.onopen = () => {
        this.wsStatus = "已连接";
        this.log("WebSocket 已连接");
        // 仅重连时全量校准；首次连接由 bootstrap.refreshAll 负责
        if (this._wsHadConnected) {
          this.safeCall(() => this.refreshAll(), "重连后同步失败");
        }
        this._wsHadConnected = true;
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
            const typ = String(evt?.type || "");
            if (typ === "profile_updated") {
              this.onProfileUpdated(evt);
              return;
            }
            if (typ === "friend_added") {
              this.onFriendAdded(evt);
              return;
            }
            if (typ === "friend_removed") {
              this.onFriendRemoved(evt);
              return;
            }
            if (typ === "friend_request_created" || typ === "friend_request_updated") {
              this.safeCall(async () => {
                await this.syncFriendRequests();
                if (typ === "friend_request_created" && Number(evt.to_user_id || 0) === Number(this.currentUser?.id || 0)) {
                  this.log("收到一条好友申请");
                }
              }, "同步好友申请失败");
              return;
            }
            if (typ.startsWith("group_")) {
              this.onGroupMetaEvent(evt);
              return;
            }
            if (evt?.group_id && evt?.content) {
              this.onGroupMessage(evt, from);
              return;
            }
          } catch (_) {}
        }
        if (!from || !content) return;
        this.onPrivateMessage(from, content);
      } catch (_) {}
    },
    onProfileUpdated(evt) {
      const uid = Number(evt.user_id || 0);
      if (!uid) return;
      const nick = String(evt.nickname || "");
      const username = String(evt.username || "");
      const avatar = String(evt.avatar_url || "");
      const list = (this.friends || []).map((f) => {
        if (Number(f.friend_id) !== uid) return f;
        return {
          ...f,
          nickname: nick,
          username: username || f.username,
          avatar_url: avatar || f.avatar_url,
        };
      });
      this.friends = list;
    },
    onFriendAdded(evt) {
      const uid = Number(evt.user_id || 0);
      if (!uid) return;
      const row = {
        friend_id: uid,
        username: String(evt.username || ""),
        nickname: String(evt.nickname || ""),
        avatar_url: String(evt.avatar_url || ""),
      };
      const list = [...(this.friends || [])];
      const idx = list.findIndex((f) => Number(f.friend_id) === uid);
      if (idx >= 0) list[idx] = { ...list[idx], ...row };
      else list.unshift(row);
      this.friends = list;
      this.safeCall(() => this.syncFriendRequests(), "同步好友申请失败");
    },
    onFriendRemoved(evt) {
      const uid = Number(evt.user_id || 0);
      if (!uid) return;
      this.friends = (this.friends || []).filter((f) => Number(f.friend_id) !== uid);
      if (Number(this.currentPeerId || 0) === uid) {
        this.currentPeerId = null;
        this.messages = [];
      }
      this.conversations = (this.conversations || []).filter((c) => Number(c.peer_id) !== uid);
      delete this.unreadMap[uid];
    },
    onGroupMetaEvent(evt) {
      const gid = Number(evt.group_id || 0);
      if (!gid) return;
      if (evt.type === "group_disbanded") {
        this.groupsLocal = (this.groupsLocal || []).filter((g) => Number(g.id) !== gid);
        delete this.groupMessagesMap[gid];
        delete this.groupDetailsMap[gid];
        delete this.groupMembersMap[gid];
        delete this.groupUnreadMap[gid];
        if (Number(this.selectedGroupId || 0) === gid) {
          this.selectedGroupId = "";
          this.showGroupPanel = false;
        }
        return;
      }
      if (evt.type === "group_member_removed" || evt.type === "group_member_left") {
        const target = Number(evt.target_user_id || evt.user_id || 0);
        if (target === Number(this.currentUser?.id || 0)) {
          this.groupsLocal = (this.groupsLocal || []).filter((g) => Number(g.id) !== gid);
          delete this.groupMessagesMap[gid];
          delete this.groupDetailsMap[gid];
          delete this.groupMembersMap[gid];
          delete this.groupUnreadMap[gid];
          if (Number(this.selectedGroupId || 0) === gid) this.selectedGroupId = "";
          if (Number(this.currentGroupID || 0) === gid) this.showGroupPanel = false;
        } else if (Number(this.selectedGroupId || 0) === gid && this.showGroupPanel) {
          this.safeCall(() => this.loadGroupMembers(gid), "刷新群成员失败");
        }
        return;
      }
      if (
        evt.type === "group_created" ||
        evt.type === "group_updated" ||
        evt.type === "group_member_added" ||
        evt.type === "group_owner_transferred"
      ) {
        const idx = (this.groupsLocal || []).findIndex((g) => Number(g.id) === gid);
        const name = String(evt.name || `群聊 #${gid}`);
        const now = Math.floor(Date.now() / 1000);
        if (idx >= 0) {
          const copy = [...this.groupsLocal];
          copy[idx] = {
            ...copy[idx],
            name,
            owner_user_id: Number(evt.owner_user_id || evt.new_owner_user_id || copy[idx].owner_user_id || 0),
            notice: String(evt.notice != null ? evt.notice : copy[idx].notice || ""),
            updated_at: now,
          };
          this.groupsLocal = copy;
        } else {
          this.groupsLocal = [
            {
              id: gid,
              name,
              owner_user_id: Number(evt.owner_user_id || evt.new_owner_user_id || 0),
              notice: String(evt.notice || ""),
              updated_at: now,
            },
            ...(this.groupsLocal || []),
          ];
        }
        if (Number(this.selectedGroupId || 0) === gid && this.showGroupPanel) {
          this.safeCall(() => this.loadGroupDetail(gid), "刷新群资料失败");
          if (evt.type === "group_member_added" || evt.type === "group_owner_transferred") {
            this.safeCall(() => this.loadGroupMembers(gid), "刷新群成员失败");
          }
        }
      }
    },
    onGroupMessage(evt, from) {
      const gid = Number(evt.group_id);
      if (!gid) return;
      if (!this.groupsLocal.find((g) => Number(g.id) === gid)) {
        this.groupsLocal = [
          {
            id: gid,
            name: String(evt.group_name || `群聊 #${gid}`),
            updated_at: Math.floor(Date.now() / 1000),
          },
          ...(this.groupsLocal || []),
        ];
      } else {
        this.touchGroupLocal(gid);
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
        mention_meta: evt.mention_meta != null ? String(evt.mention_meta) : "",
      });
      if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载文件元信息失败");
      if (Number(this.selectedGroupId || 0) !== gid) {
        this.groupUnreadMap[gid] = Number(this.groupUnreadMap[gid] || 0) + 1;
      } else {
        this.safeCall(() => this.markCurrentGroupRead(), "更新群已读失败");
      }
    },
    onPrivateMessage(from, content) {
      const payload = this.parseMessagePayload(content);
      const preview =
        payload.uiType === "file" ? `[文件] ${payload.fileName || ""}` : String(payload.text || "");
      const viewing = !this.selectedGroupId && Number(this.currentPeerId) === from;
      if (viewing) {
        this.messages.push({
          from_user_id: from,
          content: payload.text,
          isMe: false,
          ui_type: payload.uiType,
          file_id: payload.fileID,
          file_name: payload.fileName,
        });
        if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载文件元信息失败");
        this.touchPrivateConversation(from, preview);
        this.safeCall(
          () => apiRequest(`/api/v1/conversations/${encodeURIComponent(from)}/read`, { method: "PUT" }),
          "标记已读失败"
        );
        return;
      }
      this.unreadMap[from] = Number(this.unreadMap[from] || 0) + 1;
      this.touchPrivateConversation(from, preview);
      if (payload.uiType === "file" && payload.fileID) this.safeCall(() => this.ensureFileMeta(payload.fileID), "加载文件元信息失败");
    },
    touchPrivateConversation(peerID, previewText) {
      const peer = Number(peerID || 0);
      if (!peer) return;
      const now = Math.floor(Date.now() / 1000);
      const preview = String(previewText || "").slice(0, 48);
      const list = [...(this.conversations || [])];
      const idx = list.findIndex((c) => Number(c.peer_id) === peer);
      if (idx >= 0) {
        list[idx] = { ...list[idx], peer_id: peer, updated_at: now, last_preview: preview };
      } else {
        list.unshift({ peer_id: peer, updated_at: now, last_preview: preview });
      }
      this.conversations = list;
    },
    touchGroupLocal(gid) {
      const id = Number(gid || 0);
      if (!id) return;
      const now = Math.floor(Date.now() / 1000);
      const list = [...(this.groupsLocal || [])];
      const idx = list.findIndex((g) => Number(g.id) === id);
      if (idx < 0) return;
      list[idx] = { ...list[idx], updated_at: now };
      this.groupsLocal = list;
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
    async syncFriends() {
      await this.loadFriends();
    },
    async syncFriendRequests() {
      await this.loadFriendRequestsCenter();
    },
    async syncConversations() {
      await this.loadConversations();
    },
    async syncGroups() {
      await this.loadMyGroups();
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
      const preview =
        payload.uiType === "file" ? `[文件] ${payload.fileName || ""}` : String(payload.text || "");
      this.touchPrivateConversation(this.currentPeerId, preview);
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
      this.touchGroupLocal(gid);
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
            mention_meta: m.mention_meta != null ? String(m.mention_meta) : "",
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
      const g = res.group;
      if (g) {
        const gid = Number(g.id || this.currentGroupID);
        const idx = (this.groupsLocal || []).findIndex((x) => Number(x.id) === gid);
        if (idx >= 0) {
          const copy = [...this.groupsLocal];
          copy[idx] = { ...copy[idx], ...g };
          this.groupsLocal = copy;
        }
      }
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
    },
    async removeGroupMember(userID) {
      const gid = this.currentGroupID;
      const uid = Number(userID || 0);
      if (!gid || !uid) return;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/members/${encodeURIComponent(uid)}`, {
        method: "DELETE",
      });
      await this.loadGroupMembers(gid);
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
      await Promise.all([this.loadGroupDetail(gid), this.loadGroupMembers(gid)]);
    },
    async leaveCurrentGroup() {
      const gid = this.currentGroupID;
      if (!gid) return;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/leave`, { method: "POST" });
      this.closeGroupPanel();
      this.selectedGroupId = "";
      delete this.groupMessagesMap[gid];
      delete this.groupDetailsMap[gid];
      delete this.groupMembersMap[gid];
      delete this.groupUnreadMap[gid];
      this.groupsLocal = (this.groupsLocal || []).filter((g) => Number(g.id) !== gid);
    },
    async disbandCurrentGroup() {
      const gid = this.currentGroupID;
      if (!gid) return;
      await apiRequest(`/api/v1/groups/${encodeURIComponent(gid)}/disband`, { method: "POST" });
      this.closeGroupPanel();
      this.selectedGroupId = "";
      delete this.groupMessagesMap[gid];
      delete this.groupDetailsMap[gid];
      delete this.groupMembersMap[gid];
      delete this.groupUnreadMap[gid];
      this.groupsLocal = (this.groupsLocal || []).filter((g) => Number(g.id) !== gid);
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
      this.createGroupPickFilter = "";
      this.selectedFriendIDs = [];
      this.showCreateGroupModal = true;
      if (!(this.friends || []).length) {
        this.safeCall(() => this.loadFriends(), "加载好友失败");
      }
    },
    async confirmCreateGroupBySelection() {
      const members = this.selectedFriendIDs.map((x) => Number(x)).filter((x) => Number.isInteger(x) && x > 0);
      if (!members.length) throw new Error("请至少选择一位好友");
      let name = this.createGroupName.trim();
      if (!name) {
        const labels = members
          .map((id) => {
            const f = (this.friends || []).find((x) => Number(x.friend_id) === id);
            return f ? this.friendDisplayName(f) : `用户${id}`;
          })
          .slice(0, 3);
        name = labels.length ? `${labels.join("、")}的群` : `新群聊 ${Date.now().toString().slice(-4)}`;
      }
      const res = await apiRequest("/api/v1/groups", {
        method: "POST",
        body: { name, member_user_ids: members },
      });
      const group = res.group;
      this.groupsLocal = [group, ...this.groupsLocal.filter((g) => Number(g.id) !== Number(group.id))];
      this.showCreateGroupModal = false;
      this.leftTab = "sessions";
      await this.selectSession({ type: "group", id: group.id });
    },
    openAddFriendModal() {
      this.friendRequestIDInput = "";
      this.showAddFriendModal = true;
    },
    async confirmUpdateNickname() {
      const nickname = String(this.editNicknameInput || "").trim();
      const res = await apiRequest("/api/v1/me", {
        method: "PUT",
        body: { nickname },
      });
      const latest = res?.user || null;
      if (latest && Number(latest.id || 0) > 0) {
        this.currentUser = latest;
        saveAuth(this.token, latest);
      }
      this.showMyProfileModal = false;
      this.log("显示名已更新");
    },
    async confirmSendFriendRequestByID() {
      const toID = Number(this.friendRequestIDInput);
      if (!toID) return;
      await apiRequest("/api/v1/friends/requests", {
        method: "POST",
        body: { to_user_id: toID, remark: "" },
      });
      this.showAddFriendModal = false;
      this.leftTab = "contacts";
      this.contactsTab = "outgoing";
      await this.loadFriendRequestsCenter();
      this.log("好友申请已发出，等待对方处理");
    },
    openContactsNav() {
      this.leftTab = "contacts";
      this.safeCall(() => this.syncFriendRequests(), "同步好友申请失败");
    },
    openContactsTab(tab) {
      this.contactsTab = tab;
      this.safeCall(() => this.syncFriendRequests(), "同步好友申请失败");
    },
    friendRequestStatusLabel(status) {
      const s = String(status || "").toLowerCase();
      if (s === "pending") return "等待对方处理";
      if (s === "accepted") return "已同意";
      if (s === "rejected") return "已拒绝";
      if (s === "cancelled" || s === "canceled") return "已取消";
      return s ? `状态：${s}` : "状态未知";
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
      // 启动时先调用 /api/v1/me 一次：服务端用 gRPC 中间件校验 token，
      // 顺便把最新用户资料（昵称/状态）同步到前端。
      // 任意错误（401/网络/5xx）都视为登录态失效，清 auth 并回登录页。
      try {
        const meRes = await apiRequest("/api/v1/me");
        const latest = meRes?.user || null;
        if (latest && Number(latest.id || 0) > 0) {
          this.currentUser = latest;
          saveAuth(this.token, latest);
        }
      } catch (err) {
        this.log(`登录态校验失败: ${err?.message || err}`);
        clearAuth();
        location.replace("./login.html");
        return;
      }
      this.connectWs();
      await this.refreshAll();
    },
    // 已取消 3s 全量轮询；保留空实现以免旧调用报错。
    startAutoSync() {},
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
    /** 群聊 @ 分段：仅依据服务端 `mention_meta`（V1）+ content 的 rune 区间；文件/系统消息/非群聊返回 [] */
    groupMentionSegments(m) {
      if (!m || m.ui_type === "file" || m.message_type === "system") return [];
      if (!this.selectedGroupId) return [];
      const text = String(m.content || "");
      const chars = Array.from(text);
      let meta;
      try {
        meta = m.mention_meta ? JSON.parse(String(m.mention_meta)) : null;
      } catch {
        return [{ type: "plain", text }];
      }
      if (!meta || meta.v !== 1 || !Array.isArray(meta.spans)) {
        return [{ type: "plain", text }];
      }
      const myId = Number(this.currentUser?.id || 0);
      const spans = [...meta.spans].sort((a, b) => Number(a.b) - Number(b.b));
      const out = [];
      let cursor = 0;
      for (const sp of spans) {
        const b = Number(sp.b);
        const e = Number(sp.e);
        if (!Number.isFinite(b) || !Number.isFinite(e) || e <= b) continue;
        if (b < cursor || b >= chars.length) continue;
        if (b > cursor) {
          out.push({ type: "plain", text: chars.slice(cursor, b).join("") });
        }
        const end = Math.min(e, chars.length);
        const mentionText = chars.slice(b, end).join("");
        if (sp.k === "all") {
          out.push({ type: "mention", text: mentionText, kind: "all" });
        } else if (sp.k === "user") {
          const uid = Number(sp.u);
          const kind = myId && uid && uid === myId ? "me" : "user";
          out.push({ type: "mention", text: mentionText, kind });
        } else {
          out.push({ type: "plain", text: mentionText });
        }
        cursor = end;
      }
      if (cursor < chars.length) {
        out.push({ type: "plain", text: chars.slice(cursor).join("") });
      }
      return out.length ? out : [{ type: "plain", text }];
    },
    groupMentionClass(seg) {
      if (!seg || seg.type !== "mention") return "";
      const k = seg.kind || "user";
      if (k === "all") return "mention mention-all";
      if (k === "me") return "mention mention-me";
      return "mention mention-user";
    },
  };
};


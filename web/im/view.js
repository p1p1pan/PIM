window.IM_TEMPLATE = `
<div class="im-layout">
  <aside class="left-nav">
    <div class="profile">
      <div class="avatar">{{ userInitial }}</div>
      <div class="profile-name">{{ currentUser?.username || "未登录" }}</div>
      <div class="profile-status">WS: {{ wsStatus }}</div>
    </div>
    <div class="nav-list">
      <button class="nav-item" :class="{active: leftTab==='sessions'}" @click="leftTab='sessions'">
        <span>聊天</span>
      </button>
      <button class="nav-item" :class="{active: leftTab==='contacts'}" @click="leftTab='contacts'">
        <span>联系人</span>
      </button>
    </div>
    <button class="logout-btn" @click="openLogPage">管理后台</button>
    <button class="logout-btn danger-light" @click="logout">退出登录</button>
  </aside>

  <section class="panel middle-panel">
    <div class="search">
      <input v-model="searchText" :placeholder="leftTab==='sessions' ? '搜索会话 / 群聊' : '搜索联系人 / 申请'" />
      <button
        class="search-plus"
        @click="leftTab==='sessions' ? openCreateGroupModal() : openAddFriendModal()"
      >+</button>
    </div>
    <div class="list-panel" v-if="leftTab==='sessions'">
      <div class="section-title">会话列表</div>
      <div
        class="session-item"
        :class="{active: currentSessionKey===item.key}"
        v-for="item in filteredSessionItems"
        :key="item.key"
        @click="safeCall(() => selectSession(item), '切换会话失败')"
      >
        <div class="session-main">
          <div class="session-name">{{ item.title }}</div>
          <span v-if="item.unread" class="badge">{{ item.unread }}</span>
        </div>
        <div class="session-desc">{{ item.desc }}</div>
      </div>
      <div class="empty" v-if="filteredSessionItems.length===0">暂无会话，点击 + 可创建群或发起聊天</div>
    </div>

    <div class="list-panel" v-else>
      <div class="sub-tabs">
        <button :class="{active: contactsTab==='friends'}" @click="contactsTab='friends'">好友</button>
        <button :class="{active: contactsTab==='incoming'}" @click="contactsTab='incoming'">收到申请</button>
        <button :class="{active: contactsTab==='outgoing'}" @click="contactsTab='outgoing'">发出申请</button>
      </div>

      <div v-if="contactsTab==='friends'">
        <div class="section-title">我的好友</div>
        <div class="session-item" v-for="f in filteredFriends" :key="'f-'+f.friend_id" @click="safeCall(() => openPrivateById(f.friend_id), '打开会话失败')">
          <div class="session-name">用户 #{{ f.friend_id }}</div>
          <div class="inline-actions">
            <span class="muted">点击开始聊天</span>
            <button @click.stop="safeCall(() => deleteFriendByID(f.friend_id), '删除好友失败')">删除好友</button>
          </div>
        </div>
        <div class="empty" v-if="filteredFriends.length===0">暂无好友，点击 + 添加好友</div>
      </div>

      <div v-else-if="contactsTab==='incoming'">
        <div class="section-title">收到的申请</div>
        <div class="session-item" v-for="r in filteredIncomingRequests" :key="'in-'+r.request_id">
          <div class="session-name">来自 #{{ r.from_user_id }}</div>
          <div class="inline-actions">
            <button @click="safeCall(() => approveRequest(r.request_id), '同意失败')">同意</button>
            <button @click="safeCall(() => rejectRequest(r.request_id), '拒绝失败')">拒绝</button>
          </div>
        </div>
        <div class="empty" v-if="filteredIncomingRequests.length===0">暂无待处理申请</div>
      </div>

      <div v-else>
        <div class="section-title">发出的申请</div>
        <div class="session-item" v-for="r in filteredOutgoingRequests" :key="'out-'+r.request_id">
          <div class="session-name">发给 #{{ r.to_user_id }}</div>
          <div class="session-desc">状态: {{ r.status || '-' }}</div>
        </div>
        <div class="empty" v-if="filteredOutgoingRequests.length===0">暂无发出的申请</div>
      </div>
    </div>
  </section>

  <section class="panel chat-panel">
    <div class="chat-head">
      <div>
        <div class="chat-title">{{ currentTitle }}</div>
        <div class="muted">{{ selectedGroupId ? '群聊会话' : '单聊会话' }}</div>
      </div>
      <div class="head-actions">
        <button v-if="selectedGroupId" @click="safeCall(() => openGroupPanel(), '打开群资料失败')">群资料</button>
        <button @click="safeCall(() => refreshAll(), '刷新失败')">刷新</button>
        <button @click="showDebug=!showDebug">{{ showDebug ? '隐藏日志' : '调试日志' }}</button>
      </div>
    </div>
    <div class="chat-messages" ref="chatMessages" @scroll.passive="onChatScroll">
      <div class="empty" v-if="selectedGroupId && groupHistoryState[Number(selectedGroupId)]?.loading">加载历史消息中...</div>
      <div class="empty" v-if="!currentSessionKey">请选择左侧会话开始聊天</div>
      <div v-for="(m, idx) in currentChatMessages" :key="'m-'+idx" class="msg-line" :class="{me: !!m.isMe, system: m.message_type==='system'}">
        <div class="bubble-wrap">
          <div class="sender" v-if="selectedGroupId && m.message_type!=='system'">{{ m.isMe ? '我' : ('用户 #' + m.from_user_id) }}</div>
          <div class="bubble" v-if="m.ui_type!=='file'">{{ m.content }}</div>
          <div class="bubble file-bubble" v-else>
            <div class="file-name">{{ m.file_name || ('文件 #' + m.file_id) }}</div>
            <a class="file-link" v-if="fileMetaMap[m.file_id]?.status==='ok' && fileMetaMap[m.file_id]?.download_url" :href="fileMetaMap[m.file_id].download_url" target="_blank">下载文件</a>
            <span class="muted" v-else>文件处理中或不可用</span>
          </div>
        </div>
      </div>
    </div>
    <div class="composer" v-if="!selectedGroupId">
      <input v-model="msgInput" @keydown.enter.prevent="safeCall(() => sendPrivateMessage(), '发送单聊失败')" placeholder="输入消息，回车发送..." />
      <label class="upload-btn">
        文件
        <input type="file" class="hidden-file-input" @change="safeCall(() => sendFileMessageFromInput($event), '发送文件失败')" />
      </label>
      <button class="primary" :disabled="!canSendPrivate" @click="safeCall(() => sendPrivateMessage(), '发送单聊失败')">发送</button>
    </div>
    <div class="composer" v-else>
      <input v-model="groupMsg" @keydown.enter.prevent="safeCall(() => sendGroupMessage(), '发送群聊失败')" placeholder="输入群消息，回车发送..." />
      <label class="upload-btn">
        文件
        <input type="file" class="hidden-file-input" @change="safeCall(() => sendFileMessageFromInput($event), '发送文件失败')" />
      </label>
      <button class="primary" :disabled="!canSendGroup" @click="safeCall(() => sendGroupMessage(), '发送群聊失败')">发送</button>
    </div>
    <div class="logs" v-if="showDebug">{{ logs.join('\\n') }}</div>
  </section>

  <aside class="panel group-panel" v-if="showGroupPanel && selectedGroupId">
    <div class="group-panel-head">
      <div>
        <div class="chat-title">群资料</div>
        <div class="muted">群ID: {{ currentGroupID }}</div>
      </div>
      <button @click="closeGroupPanel">关闭</button>
    </div>
    <div class="group-panel-body">
      <div class="section-title">基础信息</div>
      <input
        :disabled="!isGroupOwner"
        :value="currentGroupDetail?.name || ''"
        @input="currentGroupDetail && (currentGroupDetail.name = $event.target.value)"
        placeholder="群名称"
      />
      <input
        :disabled="!isGroupOwner"
        :value="currentGroupDetail?.notice || ''"
        @input="currentGroupDetail && (currentGroupDetail.notice = $event.target.value)"
        placeholder="群公告"
      />
      <div class="muted">群主: #{{ currentGroupDetail?.owner_user_id || '-' }}</div>
        <button v-if="isGroupOwner" class="primary" @click="safeCall(() => saveGroupProfile(), '保存群资料失败')">保存资料</button>

      <div class="section-title">成员管理</div>
      <div class="input-row" v-if="isGroupOwner">
        <input v-model="groupMemberAddInput" placeholder="输入用户ID拉入群" />
        <button @click="safeCall(() => addGroupMemberByID(), '拉人失败')">拉人</button>
      </div>
      <div class="member-list">
        <div class="member-row" v-for="m in currentGroupMembers" :key="'gm-'+m.user_id">
          <div>
            <div class="session-name">用户 #{{ m.user_id }}</div>
            <div class="muted">{{ m.role }}</div>
          </div>
          <button
            v-if="isGroupOwner && Number(m.user_id)!==Number(currentUser?.id||0)"
            @click="safeCall(() => removeGroupMember(m.user_id), '移除成员失败')"
          >移除</button>
        </div>
      </div>

      <div class="section-title">群主操作</div>
      <div class="input-row" v-if="isGroupOwner">
        <input v-model="groupTransferInput" placeholder="输入新群主用户ID" />
        <button @click="safeCall(() => transferGroupOwnerByID(), '转让群主失败')">转让</button>
      </div>
      <button v-if="isGroupOwner" class="danger" @click="safeCall(() => disbandCurrentGroup(), '解散群失败')">解散群</button>
      <button v-else class="danger ghost" @click="safeCall(() => leaveCurrentGroup(), '退群失败')">退出群聊</button>
    </div>
  </aside>

  <div class="modal-mask" v-if="showCreateGroupModal" @click.self="showCreateGroupModal=false">
    <div class="modal">
      <div class="modal-title">创建群组</div>
      <input v-model="createGroupName" placeholder="群名称" />
      <div class="friend-pick-list">
        <label class="friend-pick-item" v-for="f in friends" :key="'pick-'+f.friend_id">
          <input type="checkbox" :value="Number(f.friend_id)" v-model="selectedFriendIDs" />
          <span>用户 #{{ f.friend_id }}</span>
        </label>
      </div>
      <div class="modal-actions">
        <button @click="showCreateGroupModal=false">取消</button>
        <button class="primary" @click="safeCall(() => confirmCreateGroupBySelection(), '创建群组失败')">创建</button>
      </div>
    </div>
  </div>

  <div class="modal-mask" v-if="showAddFriendModal" @click.self="showAddFriendModal=false">
    <div class="modal">
      <div class="modal-title">添加好友</div>
      <input v-model="friendRequestIDInput" placeholder="输入用户ID" />
      <div class="modal-actions">
        <button @click="showAddFriendModal=false">取消</button>
        <button class="primary" @click="safeCall(() => confirmSendFriendRequestByID(), '发送申请失败')">发送申请</button>
      </div>
    </div>
  </div>
</div>
`;

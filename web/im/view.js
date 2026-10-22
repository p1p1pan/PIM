window.IM_TEMPLATE = `
<div class="im-layout" @click="showPlusMenu=false">
  <aside class="left-nav">
    <div class="profile" role="button" tabindex="0" title="我的资料" @click="openMyProfileModal" @keydown.enter.prevent="openMyProfileModal">
      <div class="avatar">{{ userInitial }}</div>
      <div class="profile-name">{{ selfDisplayName }}</div>
    </div>
    <div class="nav-list">
      <button class="nav-item" :class="{active: leftTab==='sessions'}" @click="leftTab='sessions'">
        <span>聊天</span>
      </button>
      <button class="nav-item" :class="{active: leftTab==='contacts'}" @click="openContactsNav()">
        <span>联系人</span>
        <span class="nav-badge" v-if="incomingPendingCount>0">{{ incomingPendingCount }}</span>
      </button>
    </div>
    <button class="logout-btn danger-light" @click="logout">退出登录</button>
  </aside>

  <section class="panel middle-panel">
    <div class="search">
      <input v-model="searchText" :placeholder="leftTab==='sessions' ? '搜索会话 / 群聊' : '搜索联系人 / 申请'" />
      <div class="plus-wrap" @click.stop>
        <button class="search-plus" :title="leftTab==='sessions' ? '新建会话' : '添加联系人'" @click="showPlusMenu=!showPlusMenu">+</button>
        <div class="plus-menu" v-if="showPlusMenu">
          <template v-if="leftTab==='sessions'">
            <button type="button" @click="openCreateGroupModal(); showPlusMenu=false">
              <span class="plus-menu-title">创建群聊</span>
              <span class="plus-menu-desc">拉好友开多人会话</span>
            </button>
            <button type="button" @click="openAddFriendModal(); showPlusMenu=false">
              <span class="plus-menu-title">添加好友</span>
              <span class="plus-menu-desc">用对方用户 ID 发申请</span>
            </button>
          </template>
          <template v-else>
            <button type="button" @click="openAddFriendModal(); showPlusMenu=false">
              <span class="plus-menu-title">添加好友</span>
              <span class="plus-menu-desc">用对方用户 ID 发申请</span>
            </button>
            <button type="button" @click="openCreateGroupModal(); showPlusMenu=false">
              <span class="plus-menu-title">创建群聊</span>
              <span class="plus-menu-desc">拉好友开多人会话</span>
            </button>
          </template>
        </div>
      </div>
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
      <div class="empty empty-guide" v-if="filteredSessionItems.length===0">
        <div class="empty-title">还没有会话</div>
        <div class="empty-desc">点右上角 +，可添加好友或创建群聊</div>
      </div>
    </div>

    <div class="list-panel" v-else>
      <div class="sub-tabs">
        <button :class="{active: contactsTab==='friends'}" @click="contactsTab='friends'">好友</button>
        <button :class="{active: contactsTab==='incoming'}" @click="openContactsTab('incoming')">
          收到申请<span class="tab-badge" v-if="incomingPendingCount>0">{{ incomingPendingCount }}</span>
        </button>
        <button :class="{active: contactsTab==='outgoing'}" @click="openContactsTab('outgoing')">发出申请</button>
      </div>

      <div v-if="contactsTab==='friends'">
        <div class="section-title">我的好友</div>
        <div class="session-item" v-for="f in filteredFriends" :key="'f-'+f.friend_id" @click="safeCall(() => openPrivateById(f.friend_id), '打开会话失败')">
          <div class="session-name">{{ friendDisplayName(f) }}</div>
          <div class="inline-actions">
            <span class="muted">点击开始聊天</span>
            <button @click.stop="safeCall(() => deleteFriendByID(f.friend_id), '删除好友失败')">删除好友</button>
          </div>
        </div>
        <div class="empty empty-guide" v-if="filteredFriends.length===0">
          <div class="empty-title">还没有好友</div>
          <div class="empty-desc">点右上角 + →「添加好友」，对方同意后会出现在这里</div>
        </div>
      </div>

      <div v-else-if="contactsTab==='incoming'">
        <div class="section-title">收到的申请</div>
        <div class="session-item request-item" v-for="r in filteredIncomingRequests" :key="'in-'+r.request_id">
          <div class="session-name">{{ requestPeerName(r, 'from') }}</div>
          <div class="session-desc">想添加你为好友<span v-if="r.remark"> · {{ r.remark }}</span></div>
          <div class="inline-actions">
            <button class="primary" @click="safeCall(() => approveRequest(r.request_id), '同意失败')">同意</button>
            <button @click="safeCall(() => rejectRequest(r.request_id), '拒绝失败')">拒绝</button>
          </div>
        </div>
        <div class="empty empty-guide" v-if="filteredIncomingRequests.length===0">
          <div class="empty-title">暂无待处理申请</div>
          <div class="empty-desc">别人加你时会出现在这里</div>
        </div>
      </div>

      <div v-else>
        <div class="section-title">发出的申请</div>
        <div class="session-item request-item" v-for="r in filteredOutgoingRequests" :key="'out-'+r.request_id">
          <div class="session-name">发给 {{ requestPeerName(r, 'to') }}</div>
          <div class="session-desc" :class="'req-status-' + String(r.status || '')">{{ friendRequestStatusLabel(r.status) }}</div>
        </div>
        <div class="empty empty-guide" v-if="filteredOutgoingRequests.length===0">
          <div class="empty-title">暂无发出的申请</div>
          <div class="empty-desc">点右上角 + →「添加好友」后会出现在这里</div>
        </div>
      </div>
    </div>
  </section>

  <section class="panel chat-panel">
    <div class="ws-banner" :class="wsBannerClass" v-if="wsBannerText">{{ wsBannerText }}</div>
    <div class="chat-head">
      <div>
        <div class="chat-title">{{ currentTitle }}</div>
        <div class="muted">{{ selectedGroupId ? '群聊会话' : (currentPeerId ? '单聊会话' : '未选择会话') }}</div>
      </div>
      <div class="head-actions">
        <button v-if="selectedGroupId" @click="safeCall(() => openGroupPanel(), '打开群资料失败')">群资料</button>
        <button @click="safeCall(() => refreshAll(), '刷新失败')">刷新</button>
      </div>
    </div>
    <div class="chat-messages" ref="chatMessages" @scroll.passive="onChatScroll">
      <div class="empty" v-if="selectedGroupId && groupHistoryState[Number(selectedGroupId)]?.loading">加载历史消息中...</div>
      <div class="empty empty-guide" v-if="!currentSessionKey">
        <div class="empty-title">选择一个会话开始聊天</div>
        <div class="empty-desc">从左侧点好友或会话；没有的话先用 + 添加好友</div>
      </div>
      <div v-for="(m, idx) in currentChatMessages" :key="'m-'+idx" class="msg-line" :class="{me: !!m.isMe, system: m.message_type==='system'}">
        <div class="msg-av" v-if="m.message_type!=='system'" aria-hidden="true">{{ messageAvatarInitial(m) }}</div>
        <div class="bubble-wrap">
          <div class="sender" v-if="m.message_type!=='system' && (selectedGroupId || !m.isMe)">
            {{ m.isMe ? '我' : peerLabel(m.from_user_id) }}
          </div>
          <div class="bubble" v-if="m.ui_type!=='file'">
            <template v-if="selectedGroupId && m.message_type!=='system'">
              <template v-for="(seg, si) in groupMentionSegments(m)" :key="'s-'+idx+'-'+si">
                <span v-if="seg.type==='plain'">{{ seg.text }}</span>
                <span v-else :class="groupMentionClass(seg)">{{ seg.text }}</span>
              </template>
            </template>
            <template v-else>{{ m.content }}</template>
          </div>
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
  </section>

  <aside class="panel group-panel" v-if="showGroupPanel && selectedGroupId">
    <div class="group-panel-head">
      <div class="gp-head-main">
        <div class="gp-avatar">{{ groupInitial }}</div>
        <div class="gp-head-text">
          <div class="gp-title">{{ currentGroupDetail?.name || '群资料' }}</div>
          <div class="gp-id-row">
            <span class="muted">群 ID {{ currentGroupID }}</span>
            <button type="button" class="ghost-btn gp-copy" @click="safeCall(() => copyText(String(currentGroupID)), '复制失败')">复制</button>
          </div>
        </div>
      </div>
      <button type="button" class="gp-close" @click="closeGroupPanel">关闭</button>
    </div>
    <div class="group-panel-body">
      <section class="gp-card">
        <div class="gp-card-title">基础信息</div>
        <label class="gp-field">
          <span class="gp-label">群名称</span>
          <input
            :disabled="!isGroupOwner"
            :value="currentGroupDetail?.name || ''"
            @input="currentGroupDetail && (currentGroupDetail.name = $event.target.value)"
            placeholder="给群起个名字"
          />
        </label>
        <label class="gp-field">
          <span class="gp-label">群公告</span>
          <textarea
            class="gp-textarea"
            :disabled="!isGroupOwner"
            :value="currentGroupDetail?.notice || ''"
            @input="currentGroupDetail && (currentGroupDetail.notice = $event.target.value)"
            placeholder="可选，写给全员看的说明"
            rows="3"
          ></textarea>
        </label>
        <div class="gp-owner-line">
          <span class="gp-label">群主</span>
          <span>{{ peerLabel(currentGroupDetail?.owner_user_id) }} · #{{ currentGroupDetail?.owner_user_id || '-' }}</span>
        </div>
        <button
          v-if="isGroupOwner"
          type="button"
          class="primary gp-card-action"
          @click="safeCall(() => saveGroupProfile(), '保存群资料失败')"
        >保存资料</button>
      </section>

      <section class="gp-card">
        <div class="gp-card-head">
          <div class="gp-card-title">成员</div>
          <span class="muted">{{ currentGroupMembers.length }} 人</span>
        </div>
        <div class="input-row gp-invite" v-if="isGroupOwner">
          <input v-model="groupMemberAddInput" inputmode="numeric" placeholder="输入用户 ID 拉入群" />
          <button type="button" @click="safeCall(() => addGroupMemberByID(), '拉人失败')">拉人</button>
        </div>
        <div class="member-list">
          <div class="member-row" v-for="m in currentGroupMembers" :key="'gm-'+m.user_id">
            <div class="member-main">
              <span class="member-av">{{ memberInitial(m) }}</span>
              <div class="member-meta">
                <div class="session-name">{{ memberDisplayName(m) }}</div>
                <div class="member-sub">
                  <span class="role-pill" :class="{ owner: String(m.role||'').toLowerCase()==='owner' }">{{ groupRoleLabel(m.role) }}</span>
                  <span class="muted" v-if="memberSecondaryLine(m)">{{ memberSecondaryLine(m) }}</span>
                </div>
              </div>
            </div>
            <button
              type="button"
              class="ghost-btn"
              v-if="isGroupOwner && Number(m.user_id)!==Number(currentUser?.id||0)"
              @click="safeCall(() => removeGroupMember(m.user_id), '移除成员失败')"
            >移除</button>
          </div>
        </div>
      </section>

      <section class="gp-card gp-danger">
        <div class="gp-card-title">危险操作</div>
        <div class="gp-danger-desc" v-if="isGroupOwner">转让群主或解散后不可轻易恢复，请谨慎。</div>
        <div class="gp-danger-desc" v-else">退出后将不再收到本群消息。</div>
        <div class="input-row" v-if="isGroupOwner">
          <input v-model="groupTransferInput" inputmode="numeric" placeholder="新群主的用户 ID" />
          <button type="button" @click="safeCall(() => transferGroupOwnerByID(), '转让群主失败')">转让</button>
        </div>
        <button
          v-if="isGroupOwner"
          type="button"
          class="danger gp-danger-btn"
          @click="safeCall(() => disbandCurrentGroup(), '解散群失败')"
        >解散群</button>
        <button
          v-else
          type="button"
          class="danger ghost gp-danger-btn"
          @click="safeCall(() => leaveCurrentGroup(), '退群失败')"
        >退出群聊</button>
      </section>
    </div>
  </aside>

  <div class="modal-mask" v-if="showCreateGroupModal" @click.self="showCreateGroupModal=false">
    <div class="modal modal-create-group">
      <div class="modal-head">
        <div class="modal-title">创建群聊</div>
        <div class="modal-sub">从好友里选人，建完会直接进入群会话</div>
      </div>
      <section class="cg-card">
        <label class="modal-field">
          <span class="modal-field-label">群名称 <span class="modal-optional">可选</span></span>
          <input v-model="createGroupName" maxlength="40" placeholder="不填则按所选好友自动生成" />
        </label>
      </section>
      <section class="cg-card cg-members">
        <div class="modal-field-row">
          <span class="modal-field-label">选择成员</span>
          <span class="cg-count" :class="{ on: selectedFriendIDs.length }">已选 {{ selectedFriendIDs.length }} 人</span>
        </div>
        <div class="cg-chips" v-if="selectedFriendsForCreateGroup.length">
          <button
            type="button"
            class="cg-chip"
            v-for="f in selectedFriendsForCreateGroup"
            :key="'chip-'+f.friend_id"
            @click="toggleFriendPick(f.friend_id)"
            :title="'取消选择 ' + friendDisplayName(f)"
          >
            <span class="cg-chip-av">{{ friendInitial(f) }}</span>
            <span class="cg-chip-name">{{ friendDisplayName(f) }}</span>
            <span class="cg-chip-x" aria-hidden="true">×</span>
          </button>
        </div>
        <div class="cg-picker" v-if="friends.length">
          <div class="cg-search">
            <input v-model="createGroupPickFilter" placeholder="搜索好友…" />
          </div>
          <div class="friend-pick-list">
            <button
              type="button"
              class="friend-pick-item"
              :class="{ checked: isFriendPicked(f.friend_id) }"
              v-for="f in filteredFriendsForCreateGroup"
              :key="'pick-'+f.friend_id"
              @click="toggleFriendPick(f.friend_id)"
            >
              <span class="pick-check" aria-hidden="true"></span>
              <span class="pick-av">{{ friendInitial(f) }}</span>
              <span class="pick-meta">
                <span class="pick-name">{{ friendDisplayName(f) }}</span>
                <span class="pick-id">ID {{ f.friend_id }}</span>
              </span>
            </button>
            <div class="empty" v-if="filteredFriendsForCreateGroup.length===0">没有匹配的好友</div>
          </div>
        </div>
        <div class="empty empty-guide" v-else>
          <div class="empty-title">还没有好友，无法拉人建群</div>
          <div class="empty-desc">先添加好友，对方同意后再来创建群聊</div>
          <button type="button" class="primary" style="margin-top:12px" @click="showCreateGroupModal=false; openAddFriendModal()">去添加好友</button>
        </div>
      </section>
      <div class="modal-actions">
        <button type="button" @click="showCreateGroupModal=false">取消</button>
        <button
          type="button"
          class="primary"
          :disabled="!canConfirmCreateGroup"
          @click="safeCall(() => confirmCreateGroupBySelection(), '创建群组失败')"
        >创建群聊</button>
      </div>
    </div>
  </div>

  <div class="modal-mask" v-if="showAddFriendModal" @click.self="showAddFriendModal=false">
    <div class="modal modal-stack">
      <div class="modal-title">添加好友</div>
      <label class="modal-field">
        <span class="modal-field-label">用户 ID</span>
        <input v-model="friendRequestIDInput" inputmode="numeric" autocomplete="off" placeholder="对方的用户 ID" />
      </label>
      <div class="modal-actions">
        <button type="button" @click="showAddFriendModal=false">取消</button>
        <button type="button" class="primary" @click="safeCall(() => confirmSendFriendRequestByID(), '发送申请失败')">添加</button>
      </div>
    </div>
  </div>

  <div class="modal-mask" v-if="showMyProfileModal" @click.self="showMyProfileModal=false">
    <div class="modal modal-stack">
      <div class="modal-title">我的资料</div>
      <label class="modal-field">
        <span class="modal-field-label">显示名</span>
        <input v-model="editNicknameInput" maxlength="32" placeholder="好友看到的名字" />
      </label>
      <div class="modal-field">
        <span class="modal-field-label">用户 ID</span>
        <div class="profile-id-row">
          <code class="profile-id-value">{{ currentUser?.id || "-" }}</code>
          <button type="button" class="ghost-btn" @click.stop="safeCall(() => copyMyUserId(), '复制失败')">复制</button>
        </div>
      </div>
      <div class="modal-actions">
        <button type="button" @click="showMyProfileModal=false">关闭</button>
        <button type="button" class="primary" @click="safeCall(() => confirmUpdateNickname(), '保存失败')">保存</button>
      </div>
    </div>
  </div>
</div>
`;

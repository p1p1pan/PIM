package service

import (
	"errors"
	"strings"

	"pim/internal/group/model"
	"pim/internal/group/repo"

	"gorm.io/gorm"
)

var (
	ErrInvalidUserID         = errors.New("invalid user id")
	ErrInvalidGroupID        = errors.New("invalid group id")
	ErrInvalidGroupName      = errors.New("invalid group name")
	ErrGroupNotFound         = errors.New("group not found")
	ErrNotGroupOwner         = errors.New("operator is not group owner")
	ErrInvalidMessageContent = errors.New("invalid message content")
	ErrMessageTooLong        = errors.New("message content too long")
	ErrInvalidEventID        = errors.New("invalid event id")
	ErrNotGroupMember        = errors.New("user is not group member")
	ErrCannotLeaveAsOwner    = errors.New("owner cannot leave group directly")
	ErrInvalidEventType      = errors.New("invalid event type")
)

var allowedSystemEventTypes = map[string]struct{}{
	"group_created":           {},
	"group_updated":           {},
	"group_member_added":      {},
	"group_member_removed":    {},
	"group_member_left":       {},
	"group_owner_transferred": {},
}

type Service struct {
	repo *repo.GroupRepo
}

// NewService 创建 Group 业务服务。
func NewService(r *repo.GroupRepo) *Service {
	return &Service{repo: r}
}

// CreateGroup 创建群并自动加入 owner。
func (s *Service) CreateGroup(ownerUserID uint, name string, memberUserIDs []uint64) (*model.Group, error) {
	if ownerUserID == 0 {
		return nil, ErrInvalidUserID
	}
	if name == "" {
		return nil, ErrInvalidGroupName
	}
	// 创建群
	g, err := s.repo.CreateGroupWithOwner(ownerUserID, name)
	if err != nil {
		return nil, err
	}

	// 追加成员
	for _, uid64 := range memberUserIDs {
		uid := uint(uid64)
		if uid == 0 || uid == ownerUserID {
			continue
		}
		if err := s.repo.AddMember(g.ID, uid, "member"); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// AddMember 仅 owner 可加成员。
func (s *Service) AddMember(groupID, operatorUserID, targetUserID uint) error {
	if groupID == 0 {
		return ErrInvalidGroupID
	}
	if operatorUserID == 0 || targetUserID == 0 {
		return ErrInvalidUserID
	}
	// 获取群
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGroupNotFound
		}
		return err
	}
	// 判断是否为群主
	if g.OwnerUserID != operatorUserID {
		return ErrNotGroupOwner
	}
	// 添加成员
	return s.repo.AddMember(groupID, targetUserID, "member")
}

// RemoveMember 仅 owner 可移除成员；移除不存在成员视为幂等成功。
func (s *Service) RemoveMember(groupID, operatorUserID, targetUserID uint) error {
	if groupID == 0 {
		return ErrInvalidGroupID
	}
	if operatorUserID == 0 || targetUserID == 0 {
		return ErrInvalidUserID
	}

	// 获取群
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGroupNotFound
		}
		return err
	}
	// 判断是否为群主
	if g.OwnerUserID != operatorUserID {
		return ErrNotGroupOwner
	}
	// 禁止移除群主自己
	if targetUserID == g.OwnerUserID {
		return ErrNotGroupOwner
	}
	// 移除成员
	return s.repo.RemoveMember(groupID, targetUserID)
}

// ListMembers 查询群成员。
func (s *Service) ListMembers(groupID uint) ([]model.GroupMember, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	// 获取群
	if _, err := s.repo.GetGroupByID(groupID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	// 列出成员
	return s.repo.ListMembers(groupID)
}

// IsMember 查询是否为群成员。
func (s *Service) IsMember(groupID, userID uint) (bool, error) {
	if groupID == 0 {
		return false, ErrInvalidGroupID
	}
	if userID == 0 {
		return false, ErrInvalidUserID
	}
	// 判断是否为群成员
	return s.repo.IsMember(groupID, userID)
}

// SaveIncomingGroupMessage 校验成员身份并落库群消息。
func (s *Service) SaveIncomingGroupMessage(groupID, fromUserID uint, content, eventID string) (*model.GroupMessage, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if fromUserID == 0 {
		return nil, ErrInvalidUserID
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrInvalidMessageContent
	}
	if len(content) > 4000 {
		return nil, ErrMessageTooLong
	}
	if eventID == "" {
		return nil, ErrInvalidEventID
	}
	// 判断是否为群成员
	ok, err := s.repo.IsMember(groupID, fromUserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotGroupMember
	}
	// 事务内按 group 串行分配 seq + 通过 event_id 幂等落库。
	return s.repo.SaveGroupMessageIdempotent(groupID, fromUserID, "text", content, eventID)
}

// SaveIncomingGroupMessageBatchTrusted 批量落库（同 group）：
// 入口已做成员校验，这里仅做参数校验 + 批量幂等落库。
func (s *Service) SaveIncomingGroupMessageBatchTrusted(groupID uint, inputs []repo.SaveGroupMessageInput) ([]model.GroupMessage, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	for _, in := range inputs {
		if in.GroupID != groupID {
			return nil, ErrInvalidGroupID
		}
		if in.FromUserID == 0 {
			return nil, ErrInvalidUserID
		}
		if strings.TrimSpace(in.Content) == "" {
			return nil, ErrInvalidMessageContent
		}
		if len(in.Content) > 4000 {
			return nil, ErrMessageTooLong
		}
		if in.EventID == "" {
			return nil, ErrInvalidEventID
		}
	}
	return s.repo.SaveGroupMessageIdempotentBatch(inputs)
}

// SaveIncomingGroupMessageTrusted 用于 Kafka 内部消费链路：
// 成员校验已在网关入口完成，此处跳过 IsMember 查询，减少每条消息一次 DB 往返。
func (s *Service) SaveIncomingGroupMessageTrusted(groupID, fromUserID uint, content, eventID string) (*model.GroupMessage, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if fromUserID == 0 {
		return nil, ErrInvalidUserID
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrInvalidMessageContent
	}
	if len(content) > 4000 {
		return nil, ErrMessageTooLong
	}
	if eventID == "" {
		return nil, ErrInvalidEventID
	}
	return s.repo.SaveGroupMessageIdempotent(groupID, fromUserID, "text", content, eventID)
}

// ListMemberUserIDs 查询群成员 user_id 列表（供消息扇出）。
func (s *Service) ListMemberUserIDs(groupID uint) ([]uint, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if _, err := s.repo.GetGroupByID(groupID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return s.repo.ListMemberUserIDs(groupID)
}

// ListMemberUserIDsTrusted 供 group-message 内部消费链路使用：
// 入口已经是 trusted 路径，不再额外查询 groups 表，避免热路径慢 SQL。
func (s *Service) ListMemberUserIDsTrusted(groupID uint) ([]uint, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	return s.repo.ListMemberUserIDs(groupID)
}

// ListUserGroups 查询用户加入的群列表。
func (s *Service) ListUserGroups(userID uint) ([]model.Group, error) {
	if userID == 0 {
		return nil, ErrInvalidUserID
	}
	return s.repo.ListUserGroups(userID)
}

// ListGroupMessages 查询群消息历史（仅成员可查）。
func (s *Service) ListGroupMessages(groupID, userID uint, beforeSeq uint64, limit int) ([]model.GroupMessage, bool, uint64, error) {
	if groupID == 0 {
		return nil, false, 0, ErrInvalidGroupID
	}
	if userID == 0 {
		return nil, false, 0, ErrInvalidUserID
	}
	ok, err := s.repo.IsMember(groupID, userID)
	if err != nil {
		return nil, false, 0, err
	}
	if !ok {
		return nil, false, 0, ErrNotGroupMember
	}
	return s.repo.ListGroupMessages(groupID, beforeSeq, limit)
}

// GetGroup 查询群详情（仅成员可见）。
func (s *Service) GetGroup(groupID, operatorUserID uint) (*model.Group, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if operatorUserID == 0 {
		return nil, ErrInvalidUserID
	}
	ok, err := s.repo.IsMember(groupID, operatorUserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotGroupMember
	}
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	return g, nil
}

// UpdateGroup 更新群资料（仅群主可改）。
func (s *Service) UpdateGroup(groupID, operatorUserID uint, name, notice string) (*model.Group, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if operatorUserID == 0 {
		return nil, ErrInvalidUserID
	}
	name = strings.TrimSpace(name)
	notice = strings.TrimSpace(notice)
	if name == "" {
		return nil, ErrInvalidGroupName
	}
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	if g.OwnerUserID != operatorUserID {
		return nil, ErrNotGroupOwner
	}
	return s.repo.UpdateGroup(groupID, name, notice)
}

// LeaveGroup 退出群（群主不可直接退出，需先转让或解散）。
func (s *Service) LeaveGroup(groupID, operatorUserID uint) error {
	if groupID == 0 {
		return ErrInvalidGroupID
	}
	if operatorUserID == 0 {
		return ErrInvalidUserID
	}
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGroupNotFound
		}
		return err
	}
	if g.OwnerUserID == operatorUserID {
		return ErrCannotLeaveAsOwner
	}
	ok, err := s.repo.IsMember(groupID, operatorUserID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotGroupMember
	}
	return s.repo.LeaveGroup(groupID, operatorUserID)
}

// DisbandGroup 解散群（仅群主）。
func (s *Service) DisbandGroup(groupID, operatorUserID uint) error {
	if groupID == 0 {
		return ErrInvalidGroupID
	}
	if operatorUserID == 0 {
		return ErrInvalidUserID
	}
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGroupNotFound
		}
		return err
	}
	if g.OwnerUserID != operatorUserID {
		return ErrNotGroupOwner
	}
	return s.repo.DisbandGroup(groupID)
}

// TransferOwner 转让群主（仅群主、目标必须为群成员）。
func (s *Service) TransferOwner(groupID, operatorUserID, targetUserID uint) (*model.Group, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if operatorUserID == 0 || targetUserID == 0 {
		return nil, ErrInvalidUserID
	}
	if operatorUserID == targetUserID {
		return nil, ErrInvalidUserID
	}
	g, err := s.repo.GetGroupByID(groupID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrGroupNotFound
		}
		return nil, err
	}
	if g.OwnerUserID != operatorUserID {
		return nil, ErrNotGroupOwner
	}
	ok, err := s.repo.IsMember(groupID, targetUserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotGroupMember
	}
	return s.repo.TransferOwner(groupID, operatorUserID, targetUserID)
}

// AppendSystemMessage 写入群系统消息（操作人必须是群成员）。
func (s *Service) AppendSystemMessage(groupID, operatorUserID uint, eventType, content, eventID string) (*model.GroupMessage, error) {
	if groupID == 0 {
		return nil, ErrInvalidGroupID
	}
	if operatorUserID == 0 {
		return nil, ErrInvalidUserID
	}
	eventType = strings.TrimSpace(eventType)
	content = strings.TrimSpace(content)
	if eventType == "" {
		return nil, ErrInvalidEventType
	}
	if _, ok := allowedSystemEventTypes[eventType]; !ok {
		return nil, ErrInvalidEventType
	}
	if content == "" {
		return nil, ErrInvalidMessageContent
	}
	if eventID == "" {
		return nil, ErrInvalidEventID
	}
	ok, err := s.repo.IsMember(groupID, operatorUserID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotGroupMember
	}
	return s.repo.SaveGroupMessageIdempotent(groupID, operatorUserID, "system", content, eventID)
}

// MarkGroupRead 上报群已读游标。
func (s *Service) MarkGroupRead(groupID, userID uint, readSeq uint64) (uint64, error) {
	if groupID == 0 {
		return 0, ErrInvalidGroupID
	}
	if userID == 0 {
		return 0, ErrInvalidUserID
	}
	ok, err := s.repo.IsMember(groupID, userID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, ErrNotGroupMember
	}
	latestMap, err := s.repo.ListLatestSeqMap([]uint{groupID})
	if err != nil {
		return 0, err
	}
	latest := latestMap[groupID]
	if readSeq == 0 || readSeq > latest {
		readSeq = latest
	}
	if err := s.repo.UpsertReadSeq(groupID, userID, readSeq); err != nil {
		return 0, err
	}
	return readSeq, nil
}

// GroupConversationItem 用户群会话项。
type GroupConversationItem struct {
	Group       model.Group
	LastSeq     uint64
	ReadSeq     uint64
	UnreadCount uint64
}

// ListUserGroupConversations 查询用户群会话列表（含未读）。
func (s *Service) ListUserGroupConversations(userID uint) ([]GroupConversationItem, error) {
	if userID == 0 {
		return nil, ErrInvalidUserID
	}
	groups, err := s.repo.ListUserGroups(userID)
	if err != nil {
		return nil, err
	}
	groupIDs := make([]uint, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}
	latestSeqMap, err := s.repo.ListLatestSeqMap(groupIDs)
	if err != nil {
		return nil, err
	}
	items := make([]GroupConversationItem, 0, len(groups))
	for _, g := range groups {
		lastSeq := latestSeqMap[g.ID]
		readSeq, err := s.repo.GetReadSeq(g.ID, userID)
		if err != nil {
			return nil, err
		}
		unread := uint64(0)
		if lastSeq > readSeq {
			unread = lastSeq - readSeq
		}
		items = append(items, GroupConversationItem{
			Group:       g,
			LastSeq:     lastSeq,
			ReadSeq:     readSeq,
			UnreadCount: unread,
		})
	}
	return items, nil
}

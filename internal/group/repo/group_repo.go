package repo

import (
	"errors"
	"pim/internal/group/model"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func lockGroupSeq(tx *gorm.DB, groupID uint) error {
	// PostgreSQL 事务级 advisory lock：按 groupID 串行化 seq 分配，避免每条消息都查询/锁 groups 表。
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(groupID)).Error
}

// GroupRepo 负责 Group 领域 DB 访问。
type GroupRepo struct {
	db *gorm.DB
}

type SaveGroupMessageInput struct {
	GroupID    uint
	FromUserID uint
	Content    string
	EventID    string
}

// NewGroupRepo 创建 GroupRepo。
func NewGroupRepo(db *gorm.DB) *GroupRepo {
	return &GroupRepo{db: db}
}

// CreateGroupWithOwner 创建群并写入 owner 成员关系。
func (r *GroupRepo) CreateGroupWithOwner(ownerUserID uint, name string) (*model.Group, error) {
	var created *model.Group
	// 事务创建群和成员关系
	err := r.db.Transaction(func(tx *gorm.DB) error {
		g := &model.Group{
			Name:        name,
			OwnerUserID: ownerUserID,
		}
		// 创建群
		if err := tx.Create(g).Error; err != nil {
			return err
		}
		// 创建成员关系
		if err := tx.Create(&model.GroupMember{
			GroupID: g.ID,
			UserID:  ownerUserID,
			Role:    "owner",
		}).Error; err != nil {
			return err
		}
		created = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// AddMember 向群内添加成员。
func (r *GroupRepo) AddMember(groupID, userID uint, role string) error {
	// 幂等创建成员关系
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.GroupMember{
		GroupID: groupID,
		UserID:  userID,
		Role:    role,
	}).Error
}

// RemoveMember 移除成员。
func (r *GroupRepo) RemoveMember(groupID, userID uint) error {
	// 删除成员关系
	return r.db.Where("group_id = ? AND user_id = ?", groupID, userID).Delete(&model.GroupMember{}).Error
}

// ListMembers 列出群成员。
func (r *GroupRepo) ListMembers(groupID uint) ([]model.GroupMember, error) {
	var members []model.GroupMember
	if err := r.db.Where("group_id = ?", groupID).Order("id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	return members, nil
}

// IsMember 判断 user 是否在群中。
func (r *GroupRepo) IsMember(groupID, userID uint) (bool, error) {
	var count int64
	if err := r.db.Model(&model.GroupMember{}).
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetGroupByID 查询群信息。
func (r *GroupRepo) GetGroupByID(groupID uint) (*model.Group, error) {
	var g model.Group
	if err := r.db.First(&g, groupID).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateGroupMessage 创建群消息记录。
func (r *GroupRepo) CreateGroupMessage(msg *model.GroupMessage) error {
	return r.db.Create(msg).Error
}

// SaveGroupMessageIdempotent 在事务内完成：按 event_id 幂等、按 group 串行分配 seq、落库。
func (r *GroupRepo) SaveGroupMessageIdempotent(groupID, fromUserID uint, messageType, content, eventID string) (*model.GroupMessage, error) {
	var saved model.GroupMessage
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 先按 event_id 命中幂等记录（重复消费直接返回旧消息）。
		if err := tx.Where("event_id = ?", eventID).Limit(1).Find(&saved).Error; err == nil && saved.ID != 0 {
			// 幂等命中：同 event_id 重复写入直接复用既有记录。
			return nil
		} else if err != nil {
			return err
		}

		// 使用事务级 advisory lock 按 group 串行化 seq 分配。
		if err := lockGroupSeq(tx, groupID); err != nil {
			return err
		}

		seq := uint64(1)
		var last model.GroupMessage
		if err := tx.Where("group_id = ?", groupID).Order("seq DESC").Limit(1).Find(&last).Error; err != nil {
			return err
		}
		if last.ID != 0 {
			seq = last.Seq + 1
		}

		msg := model.GroupMessage{
			GroupID:     groupID,
			FromUserID:  fromUserID,
			MessageType: messageType,
			Content:     content,
			Seq:         seq,
			EventID:     eventID,
		}
		if err := tx.Create(&msg).Error; err != nil {
			// 并发重复 event_id 的兜底：回读已有记录。
			if err2 := tx.Where("event_id = ?", eventID).Limit(1).Find(&saved).Error; err2 != nil {
				return err2
			}
			if saved.ID != 0 {
				return nil
			}
			return err
		}
		saved = msg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// SaveGroupMessageIdempotentBatch 在单事务内批量完成同 group 消息的幂等写入与 seq 分配。
// 要求：inputs 按同 group 传入（通常由上层先按 group 分桶）。
func (r *GroupRepo) SaveGroupMessageIdempotentBatch(inputs []SaveGroupMessageInput) ([]model.GroupMessage, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	groupID := inputs[0].GroupID
	out := make([]model.GroupMessage, len(inputs))
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 1) 预查现有 event_id，命中幂等直接复用。
		eventIDs := make([]string, 0, len(inputs))
		for _, in := range inputs {
			eventIDs = append(eventIDs, in.EventID)
		}
		var exists []model.GroupMessage
		if err := tx.Where("event_id IN ?", eventIDs).Find(&exists).Error; err != nil {
			return err
		}
		existByEvent := make(map[string]model.GroupMessage, len(exists))
		for _, m := range exists {
			existByEvent[m.EventID] = m
		}

		// 2) 使用事务级 advisory lock 串行化同 group 的 seq 分配。
		if err := lockGroupSeq(tx, groupID); err != nil {
			return err
		}

		// 3) 取当前最大 seq，仅一次查询。
		nextSeq := uint64(1)
		var last model.GroupMessage
		if err := tx.Where("group_id = ?", groupID).Order("seq DESC").Limit(1).Find(&last).Error; err != nil {
			return err
		}
		if last.ID != 0 {
			nextSeq = last.Seq + 1
		}

		// 4) 构造待插入数据，未命中幂等才分配 seq。
		toCreate := make([]model.GroupMessage, 0, len(inputs))
		indexByEvent := make(map[string][]int, len(inputs))
		for i, in := range inputs {
			if m, ok := existByEvent[in.EventID]; ok {
				out[i] = m
				continue
			}
			m := model.GroupMessage{
				GroupID:     in.GroupID,
				FromUserID:  in.FromUserID,
				MessageType: "text",
				Content:     in.Content,
				Seq:         nextSeq,
				EventID:     in.EventID,
			}
			nextSeq++
			toCreate = append(toCreate, m)
			indexByEvent[in.EventID] = append(indexByEvent[in.EventID], i)
		}

		// 5) 批量插入，冲突 event_id 忽略（并发重复消费兜底）。
		if len(toCreate) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&toCreate).Error; err != nil {
				return err
			}
		}

		// 6) 回读本批 event_id，填充输出（包括 DoNothing 冲突落库于并发事务的情况）。
		var all []model.GroupMessage
		if err := tx.Where("event_id IN ?", eventIDs).Find(&all).Error; err != nil {
			return err
		}
		finalByEvent := make(map[string]model.GroupMessage, len(all))
		for _, m := range all {
			finalByEvent[m.EventID] = m
		}
		for i, in := range inputs {
			if m, ok := finalByEvent[in.EventID]; ok {
				out[i] = m
				continue
			}
			return errors.New("save group message batch: event_id not found after insert")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// NextGroupSeq 计算群内下一条序号（保留给旧逻辑，当前消息主链路已改为事务内分配）。
func (r *GroupRepo) NextGroupSeq(groupID uint) (uint64, error) {
	var last model.GroupMessage
	err := r.db.Where("group_id = ?", groupID).Order("seq DESC").First(&last).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 1, nil
		}
		return 0, err
	}
	return last.Seq + 1, nil
}

// ListMemberUserIDs 返回群内成员 user_id 列表。
func (r *GroupRepo) ListMemberUserIDs(groupID uint) ([]uint, error) {
	var members []model.GroupMember
	if err := r.db.Where("group_id = ?", groupID).Find(&members).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	return ids, nil
}

// ListUserGroups 返回某个用户加入的群列表（按最近更新倒序）。
func (r *GroupRepo) ListUserGroups(userID uint) ([]model.Group, error) {
	var groups []model.Group
	if err := r.db.
		Table("groups AS g").
		Select("g.*").
		Joins("JOIN group_members AS gm ON gm.group_id = g.id").
		Where("gm.user_id = ?", userID).
		Order("g.updated_at DESC, g.id DESC").
		Find(&groups).Error; err != nil {
		return nil, err
	}
	return groups, nil
}

// ListGroupMessages 返回群最近消息（按 seq 升序）。
func (r *GroupRepo) ListGroupMessages(groupID uint, beforeSeq uint64, limit int) ([]model.GroupMessage, bool, uint64, error) {
	if limit <= 0 {
		limit = 50
	}
	var msgs []model.GroupMessage
	q := r.db.Where("group_id = ?", groupID)
	if beforeSeq > 0 {
		q = q.Where("seq < ?", beforeSeq)
	}
	if err := q.
		Order("seq DESC, id DESC").
		Limit(limit + 1).
		Find(&msgs).Error; err != nil {
		return nil, false, 0, err
	}
	hasMore := false
	if len(msgs) > limit {
		hasMore = true
		msgs = msgs[:limit]
	}
	slices.Reverse(msgs)
	var nextBefore uint64
	if len(msgs) > 0 {
		nextBefore = msgs[0].Seq
	}
	return msgs, hasMore, nextBefore, nil
}

// GetMember 查询群成员关系。
func (r *GroupRepo) GetMember(groupID, userID uint) (*model.GroupMember, error) {
	var m model.GroupMember
	if err := r.db.Where("group_id = ? AND user_id = ?", groupID, userID).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateGroup 更新群名称和公告。
func (r *GroupRepo) UpdateGroup(groupID uint, name, notice string) (*model.Group, error) {
	if err := r.db.Model(&model.Group{}).Where("id = ?", groupID).Updates(map[string]interface{}{
		"name":   name,
		"notice": notice,
	}).Error; err != nil {
		return nil, err
	}
	return r.GetGroupByID(groupID)
}

// LeaveGroup 退出群（删除成员关系）。
func (r *GroupRepo) LeaveGroup(groupID, userID uint) error {
	return r.db.Where("group_id = ? AND user_id = ?", groupID, userID).Delete(&model.GroupMember{}).Error
}

// DisbandGroup 解散群（事务删除成员、消息、群）。
func (r *GroupRepo) DisbandGroup(groupID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&model.GroupMember{}).Error; err != nil {
			return err
		}
		if err := tx.Where("group_id = ?", groupID).Delete(&model.GroupMessage{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&model.Group{}, groupID).Error; err != nil {
			return err
		}
		return nil
	})
}

// TransferOwner 转让群主（事务更新 owner 与角色）。
func (r *GroupRepo) TransferOwner(groupID, oldOwnerID, newOwnerID uint) (*model.Group, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Group{}).Where("id = ?", groupID).Update("owner_user_id", newOwnerID).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.GroupMember{}).Where("group_id = ? AND user_id = ?", groupID, oldOwnerID).Update("role", "member").Error; err != nil {
			return err
		}
		if err := tx.Model(&model.GroupMember{}).Where("group_id = ? AND user_id = ?", groupID, newOwnerID).Update("role", "owner").Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.GetGroupByID(groupID)
}

// CountMembers 统计群成员数量。
func (r *GroupRepo) CountMembers(groupID uint) (int64, error) {
	var count int64
	if err := r.db.Model(&model.GroupMember{}).Where("group_id = ?", groupID).Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// GetReadSeq 获取用户在群内的已读游标。
func (r *GroupRepo) GetReadSeq(groupID, userID uint) (uint64, error) {
	var st model.GroupReadState
	if err := r.db.Where("group_id = ? AND user_id = ?", groupID, userID).First(&st).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return st.ReadSeq, nil
}

// UpsertReadSeq 更新用户已读游标（只允许前进）。
func (r *GroupRepo) UpsertReadSeq(groupID, userID uint, readSeq uint64) error {
	current, err := r.GetReadSeq(groupID, userID)
	if err != nil {
		return err
	}
	if readSeq < current {
		readSeq = current
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "group_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"read_seq": readSeq}),
	}).Create(&model.GroupReadState{
		GroupID: groupID,
		UserID:  userID,
		ReadSeq: readSeq,
	}).Error
}

// ListLatestSeqMap 查询多个群的最大 seq。
func (r *GroupRepo) ListLatestSeqMap(groupIDs []uint) (map[uint]uint64, error) {
	out := make(map[uint]uint64, len(groupIDs))
	if len(groupIDs) == 0 {
		return out, nil
	}
	type row struct {
		GroupID uint
		MaxSeq  uint64
	}
	var rows []row
	if err := r.db.Model(&model.GroupMessage{}).
		Select("group_id, MAX(seq) AS max_seq").
		Where("group_id IN ?", groupIDs).
		Group("group_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GroupID] = r.MaxSeq
	}
	return out, nil
}

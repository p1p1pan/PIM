package repo

import (
	"errors"
	"sort"
	"strings"

	friendmodel "pim/internal/friend/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"pim/internal/conversation/model"
)

type Repo struct {
	db *gorm.DB
}

// SendInput 是批量幂等落库输入。
type SendInput struct {
	FromID      uint
	ToID        uint
	Content     string
	ClientMsgID string
}

// NewRepo 创建会话仓储。
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// DB 返回底层数据库实例（仅在必要场景使用）。
func (r *Repo) DB() *gorm.DB { return r.db }

// ListMessages 查询两人消息，按 seq 和时间升序。
func (r *Repo) ListMessages(userID, otherID uint) ([]model.Message, error) {
	var messages []model.Message
	if err := r.db.Where("(from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?)",
		userID, otherID, otherID, userID).
		Order("seq ASC").
		Order("created_at ASC").
		Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

// ListConversations 查询用户会话列表，按最近消息倒序。
func (r *Repo) ListConversations(userID uint) ([]model.Conversation, error) {
	var convs []model.Conversation
	if err := r.db.Where("user_a = ? OR user_b = ?", userID, userID).
		Order("last_message_at DESC").
		Find(&convs).Error; err != nil {
		return nil, err
	}
	return convs, nil
}

// SendMessageIdempotent 事务化处理幂等、入库与会话更新。
func (r *Repo) SendMessageIdempotent(fromID, toID uint, content string, clientMsgID string) (*model.Message, bool, error) {
	var resultMsg *model.Message
	var created bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if clientMsgID != "" {
			var existing model.Message
			if err := tx.Where("from_user_id = ? AND client_msg_id = ?", fromID, clientMsgID).First(&existing).Error; err == nil {
				resultMsg = &existing
				created = false
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		var fr friendmodel.Friend
		if err := tx.Where("user_id = ? AND friend_id = ?", fromID, toID).First(&fr).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("not friends, cannot send message")
			}
			return err
		}

		userA, userB := fromID, toID
		if userA > userB {
			userA, userB = userB, userA
		}
		var maxSeq uint
		if err := tx.Model(&model.Message{}).
			Select("COALESCE(MAX(seq), 0)").
			Where("(from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?)",
				userA, userB, userB, userA).
			Scan(&maxSeq).Error; err != nil {
			return err
		}

		m := &model.Message{FromUserID: fromID, ToUserID: toID, Content: content, ClientMsgID: clientMsgID, Seq: maxSeq + 1}
		if err := tx.Create(m).Error; err != nil {
			if clientMsgID != "" {
				var again model.Message
				if err2 := tx.Where("from_user_id = ? AND client_msg_id = ?", fromID, clientMsgID).First(&again).Error; err2 == nil {
					resultMsg = &again
					created = false
					return nil
				}
			}
			return err
		}

		var conv model.Conversation
		if err := tx.Where("user_a = ? AND user_b = ?", userA, userB).First(&conv).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				conv = model.Conversation{UserA: userA, UserB: userB, LastMessageID: m.ID, LastSeq: m.Seq, LastMessageAt: m.CreatedAt}
				if err := tx.Create(&conv).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		} else {
			conv.LastMessageID = m.ID
			conv.LastSeq = m.Seq
			conv.LastMessageAt = m.CreatedAt
			if err := tx.Save(&conv).Error; err != nil {
				return err
			}
		}
		resultMsg = m
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return resultMsg, created, nil
}

// SendMessageIdempotentBatch 批量处理消息，保持输入顺序，批内复用好友检查与会话状态。
func (r *Repo) SendMessageIdempotentBatch(inputs []SendInput) ([]*model.Message, []bool, error) {
	msgs := make([]*model.Message, len(inputs))
	created := make([]bool, len(inputs))
	if len(inputs) == 0 {
		return msgs, created, nil
	}
	ordered := make([]batchInput, 0, len(inputs))
	for i, in := range inputs {
		ordered = append(ordered, batchInput{idx: i, in: in, conv: orderedPair(in.FromID, in.ToID)})
	}
	// 先按会话 key 排序，让同会话写入聚集，降低批内随机页访问与锁切换。
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].conv.a != ordered[j].conv.a {
			return ordered[i].conv.a < ordered[j].conv.a
		}
		if ordered[i].conv.b != ordered[j].conv.b {
			return ordered[i].conv.b < ordered[j].conv.b
		}
		return ordered[i].idx < ordered[j].idx
	})

	err := r.db.Transaction(func(tx *gorm.DB) error {
		convCache := make(map[pair]*model.Conversation)
		dirtyConv := make(map[pair]*model.Conversation)
		friendCache := make(map[pair]bool)
		existingByClientMsgID := make(map[string]*model.Message)

		// 预取本批 client_msg_id，避免逐条 select。
		clientIDs := make([]string, 0, len(inputs))
		seenClientID := make(map[string]struct{})
		friendPairs := make([]pair, 0, len(inputs))
		seenFriendPair := make(map[pair]struct{})
		convPairs := make([]pair, 0, len(inputs))
		seenConvPair := make(map[pair]struct{})
		for _, it := range ordered {
			in := it.in
			if in.ClientMsgID != "" {
				if _, ok := seenClientID[in.ClientMsgID]; !ok {
					seenClientID[in.ClientMsgID] = struct{}{}
					clientIDs = append(clientIDs, in.ClientMsgID)
				}
			}
			fp := pair{a: in.FromID, b: in.ToID}
			if _, ok := seenFriendPair[fp]; !ok {
				seenFriendPair[fp] = struct{}{}
				friendPairs = append(friendPairs, fp)
			}
			cp := it.conv
			if _, ok := seenConvPair[cp]; !ok {
				seenConvPair[cp] = struct{}{}
				convPairs = append(convPairs, cp)
			}
		}

		if len(clientIDs) > 0 {
			var existing []model.Message
			if err := tx.Where("client_msg_id IN ?", clientIDs).Find(&existing).Error; err != nil {
				return err
			}
			for i := range existing {
				m := existing[i]
				existingByClientMsgID[m.ClientMsgID] = &m
			}
		}
		if where, args := buildPairOrWhere("user_id", "friend_id", friendPairs); where != "" {
			var friends []friendmodel.Friend
			if err := tx.Where(where, args...).Find(&friends).Error; err != nil {
				return err
			}
			for _, fr := range friends {
				friendCache[pair{a: fr.UserID, b: fr.FriendID}] = true
			}
		}
		if where, args := buildPairOrWhere("user_a", "user_b", convPairs); where != "" {
			var convs []model.Conversation
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(where, args...).Find(&convs).Error; err != nil {
				return err
			}
			for i := range convs {
				c := convs[i]
				convCache[pair{a: c.UserA, b: c.UserB}] = &c
			}
		}

		for _, it := range ordered {
			i, in := it.idx, it.in
			if in.ClientMsgID != "" {
				if existing := existingByClientMsgID[in.ClientMsgID]; existing != nil {
					msgs[i] = existing
					created[i] = false
					continue
				}
			}

			if !friendCache[pair{a: in.FromID, b: in.ToID}] {
				return errors.New("not friends, cannot send message")
			}

			k := it.conv
			conv := convCache[k]
			if conv == nil {
				var tmp model.Conversation
				err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
					Where("user_a = ? AND user_b = ?", k.a, k.b).
					First(&tmp).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						tmp = model.Conversation{UserA: k.a, UserB: k.b, LastSeq: 0}
						if err := tx.Create(&tmp).Error; err != nil {
							return err
						}
					} else {
						return err
					}
				}
				conv = &tmp
				convCache[k] = conv
			}

			nextSeq := conv.LastSeq + 1
			m := &model.Message{
				FromUserID:  in.FromID,
				ToUserID:    in.ToID,
				Content:     in.Content,
				ClientMsgID: in.ClientMsgID,
				Seq:         nextSeq,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "client_msg_id"}},
				DoNothing: true,
			}).Create(m).Error; err != nil {
				return err
			}
			if m.ID == 0 && in.ClientMsgID != "" {
				var existing model.Message
				if err := tx.Where("client_msg_id = ?", in.ClientMsgID).First(&existing).Error; err != nil {
					return err
				}
				msgs[i] = &existing
				existingByClientMsgID[in.ClientMsgID] = &existing
				created[i] = false
				continue
			}

			conv.LastMessageID = m.ID
			conv.LastSeq = m.Seq
			conv.LastMessageAt = m.CreatedAt
			// 同一批内同会话可能多条消息，仅在批末统一 flush，减少热点行反复 UPDATE。
			dirtyConv[k] = conv
			msgs[i] = m
			if in.ClientMsgID != "" {
				existingByClientMsgID[in.ClientMsgID] = m
			}
			created[i] = true
		}
		for _, conv := range dirtyConv {
			if err := tx.Model(&model.Conversation{}).Where("id = ?", conv.ID).Updates(map[string]interface{}{
				"last_message_id": conv.LastMessageID,
				"last_seq":        conv.LastSeq,
				"last_message_at": conv.LastMessageAt,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return msgs, created, nil
}

// GetConversationByUsers 查询指定 userA/userB 的会话记录。
func (r *Repo) GetConversationByUsers(userA, userB uint) (*model.Conversation, error) {
	var conv model.Conversation
	if err := r.db.Where("user_a = ? AND user_b = ?", userA, userB).First(&conv).Error; err != nil {
		return nil, err
	}
	return &conv, nil
}

// UpsertReadCursor 更新或创建已读游标，只允许前进不回退。
func (r *Repo) UpsertReadCursor(conversationID, userID, lastReadSeq uint) error {
	var mr model.MessageRead
	if err := r.db.Where("conversation_id = ? AND user_id = ?", conversationID, userID).First(&mr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			mr = model.MessageRead{ConversationID: conversationID, UserID: userID, LastReadSeq: lastReadSeq}
			return r.db.Create(&mr).Error
		}
		return err
	}
	if lastReadSeq > mr.LastReadSeq {
		mr.LastReadSeq = lastReadSeq
		return r.db.Save(&mr).Error
	}
	return nil
}

type pair struct{ a, b uint }

type batchInput struct {
	idx  int
	in   SendInput
	conv pair
}

func orderedPair(a, b uint) pair {
	if a > b {
		a, b = b, a
	}
	return pair{a: a, b: b}
}

func buildPairOrWhere(colA, colB string, pairs []pair) (string, []interface{}) {
	if len(pairs) == 0 {
		return "", nil
	}
	var sb strings.Builder
	args := make([]interface{}, 0, len(pairs)*2)
	for i, p := range pairs {
		if i > 0 {
			sb.WriteString(" OR ")
		}
		sb.WriteString("(")
		sb.WriteString(colA)
		sb.WriteString(" = ? AND ")
		sb.WriteString(colB)
		sb.WriteString(" = ?)")
		args = append(args, p.a, p.b)
	}
	return sb.String(), args
}

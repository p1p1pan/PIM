package repo

import (
	"errors"

	friendmodel "pim/internal/friend/model"

	"gorm.io/gorm"

	"pim/internal/conversation/model"
)

type Repo struct {
	db *gorm.DB
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

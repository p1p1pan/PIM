package conversation

import (
	"errors"

	"gorm.io/gorm"

	"pim/internal/friend"
)

// Service 封装会话/消息业务：查两人历史消息、发消息（先校验好友再落库），供 HTTP 或 WebSocket handler 复用。
type Service struct {
	db *gorm.DB
}

// NewService 根据 db 构造 Service。
func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// ListMessages 返回两个用户之间的历史消息（按时间升序）。
func (s *Service) ListMessages(userID, otherID uint) ([]Message, error) {
	var messages []Message
	if err := s.db.
		Where("(from_user_id = ? AND to_user_id = ?) OR (from_user_id = ? AND to_user_id = ?)",
			userID, otherID, otherID, userID).
		Order("created_at ASC").
		Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}

// SendMessage 校验好友关系，保存消息并返回保存后的记录。
func (s *Service) SendMessage(fromID, toID uint, content string) (*Message, error) {
	if fromID == 0 || toID == 0 {
		return nil, errors.New("invalid user id")
	}
	if content == "" {
		return nil, errors.New("content is empty")
	}

	var fr friend.Friend
	if err := s.db.Where("user_id = ? AND friend_id = ?", fromID, toID).First(&fr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("not friends, cannot send message")
		}
		return nil, err
	}

	m := &Message{
		FromUserID: fromID,
		ToUserID:   toID,
		Content:    content,
	}
	if err := s.db.Create(m).Error; err != nil {
		return nil, err
	}
	return m, nil
}


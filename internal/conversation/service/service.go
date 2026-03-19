package service

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"pim/internal/conversation/model"
	"pim/internal/conversation/repo"
)

var (
	// ErrInvalidUserID 表示用户 ID 非法。
	ErrInvalidUserID = errors.New("invalid user id")
	// ErrEmptyContent 表示消息内容为空。
	ErrEmptyContent  = errors.New("content is empty")
	// ErrNotFriends 表示双方不是好友。
	ErrNotFriends    = errors.New("not friends, cannot send message")
)

// Service 封装会话领域业务编排。
type Service struct {
	repo *repo.Repo
}

// NewService 创建会话服务实例。
func NewService(r *repo.Repo) *Service { return &Service{repo: r} }

// ListMessages 查询两人历史消息。
func (s *Service) ListMessages(userID, otherID uint) ([]model.Message, error) {
	return s.repo.ListMessages(userID, otherID)
}

// SendMessage 发送消息（非幂等入口，内部走幂等方法）。
func (s *Service) SendMessage(fromID, toID uint, content string) (*model.Message, error) {
	m, _, err := s.SendMessageIdempotent(fromID, toID, content, "")
	return m, err
}

// SendMessageIdempotent 发送消息并处理幂等。
func (s *Service) SendMessageIdempotent(fromID, toID uint, content string, clientMsgID string) (*model.Message, bool, error) {
	if fromID == 0 || toID == 0 {
		return nil, false, ErrInvalidUserID
	}
	if content == "" {
		return nil, false, ErrEmptyContent
	}
	m, created, err := s.repo.SendMessageIdempotent(fromID, toID, content, clientMsgID)
	if err != nil {
		if err.Error() == ErrNotFriends.Error() {
			return nil, false, ErrNotFriends
		}
		return nil, false, err
	}
	return m, created, nil
}

// ListConversations 查询用户会话列表。
func (s *Service) ListConversations(userID uint) ([]model.Conversation, error) {
	if userID == 0 {
		return nil, ErrInvalidUserID
	}
	return s.repo.ListConversations(userID)
}

// HandleReadEvent 处理会话已读事件并推进已读游标。
func (s *Service) HandleReadEvent(userID uint, conversationID string) (uint, error) {
	parts := [2]uint{}
	var a, b uint
	_, err := fmt.Sscanf(conversationID, "%d:%d", &a, &b)
	if err != nil {
		return 0, nil
	}
	parts[0], parts[1] = a, b
	conv, err := s.repo.GetConversationByUsers(parts[0], parts[1])
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	lastReadSeq := conv.LastSeq
	if err := s.repo.UpsertReadCursor(conv.ID, userID, lastReadSeq); err != nil {
		return 0, err
	}
	return lastReadSeq, nil
}

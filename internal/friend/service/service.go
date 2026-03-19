package service

import (
	"errors"

	"pim/internal/friend/model"
	"pim/internal/friend/repo"
)

type Service struct {
	repo *repo.FriendRepo
}

// NewService 创建好友业务服务。
func NewService(r *repo.FriendRepo) *Service {
	return &Service{repo: r}
}

// AddFriend 添加好友并维护缓存失效。
func (s *Service) AddFriend(userID, friendID uint) error {
	if userID == 0 || friendID == 0 {
		return errors.New("invalid user id")
	}
	if userID == friendID {
		return errors.New("cannot add yourself as friend")
	}
	count, err := s.repo.CountRelation(userID, friendID)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if err := s.repo.CreateBidirectional(userID, friendID); err != nil {
		return err
	}
	s.repo.DelCache(userID)
	s.repo.DelCache(friendID)
	return nil
}

// ListFriends 查询好友列表，优先走缓存。
func (s *Service) ListFriends(userID uint) ([]model.Friend, error) {
	if cached, ok := s.repo.GetCache(userID); ok {
		return cached, nil
	}
	friends, err := s.repo.ListByUserID(userID)
	if err != nil {
		return nil, err
	}
	s.repo.SetCache(userID, friends)
	return friends, nil
}

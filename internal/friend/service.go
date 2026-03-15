package friend

import (
	"errors"

	"gorm.io/gorm"
)

// Service 封装好友域业务：加好友（双向写 friends 表）、拉好友列表，供 HTTP handler 或后续 gRPC 复用。
type Service struct {
	db *gorm.DB
}

// NewService 根据 db 构造 Service。
func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// AddFriend 为两个用户建立双向好友关系（A-B, B-A）。
func (s *Service) AddFriend(userID, friendID uint) error {
	if userID == 0 || friendID == 0 {
		return errors.New("invalid user id")
	}
	if userID == friendID {
		return errors.New("cannot add yourself as friend")
	}

	// 简单去重：如果已存在则直接返回成功
	var count int64
	if err := s.db.Model(&Friend{}).
		Where("user_id = ? AND friend_id = ?", userID, friendID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&Friend{UserID: userID, FriendID: friendID}).Error; err != nil {
			return err
		}
		if err := tx.Create(&Friend{UserID: friendID, FriendID: userID}).Error; err != nil {
			return err
		}
		return nil
	})
}

// ListFriends 返回指定用户的好友关系列表。
func (s *Service) ListFriends(userID uint) ([]Friend, error) {
	var friends []Friend
	if err := s.db.Where("user_id = ?", userID).Find(&friends).Error; err != nil {
		return nil, err
	}
	return friends, nil
}


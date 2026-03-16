package friend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Service 封装好友域业务：加好友（双向写 friends 表）、拉好友列表，供 HTTP handler 或后续 gRPC 复用。
type Service struct {
	db  *gorm.DB
	rdb *redis.Client
}

// NewService 根据 db 构造 Service。
func NewService(db *gorm.DB, rdb *redis.Client) *Service {
	return &Service{db: db, rdb: rdb}
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
		if s.rdb != nil {
			ctx := context.Background()
			_ = s.rdb.Del(ctx, fmt.Sprintf("friend:list:%d", userID)).Err()
			_ = s.rdb.Del(ctx, fmt.Sprintf("friend:list:%d", friendID)).Err()
		}
		return nil
	})
}

// ListFriends 返回指定用户的好友关系列表。
func (s *Service) ListFriends(userID uint) ([]Friend, error) {
	ctx := context.Background()
	key := fmt.Sprintf("friend:list:%d", userID)
	// 1. 先尝试从 Redis 读缓存
	if s.rdb != nil {
		if data, err := s.rdb.Get(ctx, key).Bytes(); err == nil && len(data) > 0 {
			var cached []Friend
			if err := json.Unmarshal(data, &cached); err == nil {
				return cached, nil
			}
			// 反序列化失败就继续走 DB，顺便可以考虑删掉坏缓存
			_ = s.rdb.Del(ctx, key).Err()
		}
	}
	// 2. 缓存不存在或失败，查数据库
	var friends []Friend
	if err := s.db.Where("user_id = ?", userID).Find(&friends).Error; err != nil {
		return nil, err
	}
	// 3. 写入缓存（忽略写失败）
	if s.rdb != nil {
		if data, err := json.Marshal(friends); err == nil {
			_ = s.rdb.Set(ctx, key, data, 0).Err()
		}
	}
	return friends, nil
}

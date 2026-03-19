package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"pim/internal/friend/model"
)

type FriendRepo struct {
	db  *gorm.DB
	rdb *redis.Client
}

// NewFriendRepo 创建好友仓储，统一管理 DB/Redis 访问。
func NewFriendRepo(db *gorm.DB, rdb *redis.Client) *FriendRepo {
	return &FriendRepo{db: db, rdb: rdb}
}

// CountRelation 统计两人好友关系是否已存在。
func (r *FriendRepo) CountRelation(userID, friendID uint) (int64, error) {
	var count int64
	err := r.db.Model(&model.Friend{}).
		Where("user_id = ? AND friend_id = ?", userID, friendID).
		Count(&count).Error
	return count, err
}

// CreateBidirectional 建立双向好友关系（A->B, B->A）。
func (r *FriendRepo) CreateBidirectional(userID, friendID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.Friend{UserID: userID, FriendID: friendID}).Error; err != nil {
			return err
		}
		if err := tx.Create(&model.Friend{UserID: friendID, FriendID: userID}).Error; err != nil {
			return err
		}
		return nil
	})
}

// ListByUserID 查询某用户全部好友关系。
func (r *FriendRepo) ListByUserID(userID uint) ([]model.Friend, error) {
	var friends []model.Friend
	if err := r.db.Where("user_id = ?", userID).Find(&friends).Error; err != nil {
		return nil, err
	}
	return friends, nil
}

// DelCache 删除好友列表缓存。
func (r *FriendRepo) DelCache(userID uint) {
	if r.rdb == nil {
		return
	}
	ctx := context.Background()
	_ = r.rdb.Del(ctx, fmt.Sprintf("friend:list:%d", userID)).Err()
}

// GetCache 获取好友列表缓存。
func (r *FriendRepo) GetCache(userID uint) ([]model.Friend, bool) {
	if r.rdb == nil {
		return nil, false
	}
	ctx := context.Background()
	key := fmt.Sprintf("friend:list:%d", userID)
	data, err := r.rdb.Get(ctx, key).Bytes()
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var cached []model.Friend
	if err := json.Unmarshal(data, &cached); err != nil {
		_ = r.rdb.Del(ctx, key).Err()
		return nil, false
	}
	return cached, true
}

// SetCache 写入好友列表缓存。
func (r *FriendRepo) SetCache(userID uint, friends []model.Friend) {
	if r.rdb == nil {
		return
	}
	ctx := context.Background()
	key := fmt.Sprintf("friend:list:%d", userID)
	if data, err := json.Marshal(friends); err == nil {
		_ = r.rdb.Set(ctx, key, data, 0).Err()
	}
}

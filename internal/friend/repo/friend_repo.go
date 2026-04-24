package repo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Friend{UserID: userID, FriendID: friendID}).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.Friend{UserID: friendID, FriendID: userID}).Error; err != nil {
			return err
		}
		return nil
	})
}

// DeleteBidirectional 删除双向好友关系（A->B, B->A）。
func (r *FriendRepo) DeleteBidirectional(userID, friendID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND friend_id = ?", userID, friendID).Delete(&model.Friend{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND friend_id = ?", friendID, userID).Delete(&model.Friend{}).Error; err != nil {
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

// IsBlocked 判断 userID 是否已拉黑 targetID。
func (r *FriendRepo) IsBlocked(userID, targetID uint) (bool, error) {
	var count int64
	err := r.db.Model(&model.Blacklist{}).
		Where("user_id = ? AND blocked_user_id = ?", userID, targetID).
		Count(&count).Error
	return count > 0, err
}

// HasPendingFriendRequest 是否已存在 from -> to 且状态为 pending 的申请。
func (r *FriendRepo) HasPendingFriendRequest(fromID, toID uint) (bool, error) {
	var n int64
	err := r.db.Model(&model.FriendRequest{}).
		Where("from_user_id = ? AND to_user_id = ? AND status = ?", fromID, toID, "pending").
		Count(&n).Error
	return n > 0, err
}

// CreateFriendRequest 创建好友申请。
func (r *FriendRepo) CreateFriendRequest(fromID, toID uint, remark string) (*model.FriendRequest, error) {
	req := &model.FriendRequest{
		FromUserID: fromID,
		ToUserID:   toID,
		Status:     "pending",
		Remark:     remark,
	}
	if err := r.db.Create(req).Error; err != nil {
		return nil, err
	}
	return req, nil
}

// GetFriendRequestByID 按 ID 查询好友申请。
func (r *FriendRepo) GetFriendRequestByID(id uint) (*model.FriendRequest, error) {
	var req model.FriendRequest
	if err := r.db.First(&req, id).Error; err != nil {
		return nil, err
	}
	return &req, nil
}

// UpdateFriendRequestStatus 更新申请状态。
func (r *FriendRepo) UpdateFriendRequestStatus(id uint, status string) error {
	return r.db.Model(&model.FriendRequest{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status": status,
		}).Error
}

// BlockUser 创建拉黑关系。
func (r *FriendRepo) BlockUser(userID, blockedUserID uint) error {
	return r.db.Create(&model.Blacklist{
		UserID:        userID,
		BlockedUserID: blockedUserID,
	}).Error
}

// ListIncomingFriendRequests 查询“我收到的”好友申请（按 ID 倒序分页）。
func (r *FriendRepo) ListIncomingFriendRequests(userID uint, status string, cmp uint, limit int) ([]model.FriendRequest, uint, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	// 查询“我收到的”好友申请
	q := r.db.Model(&model.FriendRequest{}).
		Where("to_user_id = ?", userID)
	// status 为空表示全部
	if status != "" {
		q = q.Where("status = ?", status)
	}
	// cmp 语义：拿比 cmp 小的旧数据（ID 倒序）
	if cmp > 0 {
		q = q.Where("id < ?", cmp)
	}
	// 查询数据
	var rows []model.FriendRequest
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, 0, false, err
	}
	// 是否有更多数据
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	// 下一个游标
	var nextCmp uint
	if len(rows) > 0 {
		nextCmp = rows[len(rows)-1].ID
	}
	return rows, nextCmp, hasMore, nil
}

// ListOutgoingFriendRequests 查询“我发出的”好友申请（按 ID 倒序分页）。
func (r *FriendRepo) ListOutgoingFriendRequests(userID uint, status string, cmp uint, limit int) ([]model.FriendRequest, uint, bool, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	// 查询“我发出的”好友申请
	q := r.db.Model(&model.FriendRequest{}).
		Where("from_user_id = ?", userID)
	// status 为空表示全部
	if status != "" {
		q = q.Where("status = ?", status)
	}
	// cmp 语义：拿比 cmp 小的旧数据（ID 倒序）
	if cmp > 0 {
		q = q.Where("id < ?", cmp)
	}
	// 查询数据
	var rows []model.FriendRequest
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, 0, false, err
	}
	// 是否有更多数据
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	// 下一个游标
	var nextCmp uint
	if len(rows) > 0 {
		nextCmp = rows[len(rows)-1].ID
	}
	return rows, nextCmp, hasMore, nil
}

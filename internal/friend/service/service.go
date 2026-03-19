package service

import (
	"errors"
	"pim/internal/friend/model"
	"pim/internal/friend/repo"
	"strings"

	"gorm.io/gorm"
)

var (
	ErrInvalidUserID       = errors.New("invalid user id")
	ErrCannotAddSelf       = errors.New("cannot add yourself as friend")
	ErrAlreadyFriends      = errors.New("already friends")
	ErrBlocked             = errors.New("blocked relationship exists")
	ErrRequestNotFound     = errors.New("friend request not found")
	ErrRequestStateInvalid = errors.New("friend request status invalid")
	ErrInvalidStatus       = errors.New("invalid friend request status")
)

type Service struct {
	repo *repo.FriendRepo
}

// NewService 创建好友业务服务。
func NewService(r *repo.FriendRepo) *Service {
	return &Service{repo: r}
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

// ListIncomingFriendRequests 查询我收到的好友申请。
func (s *Service) ListIncomingFriendRequests(userID uint, status string, cursor uint, limit int) ([]model.FriendRequest, uint, bool, error) {
	// 非法用户 ID
	if userID == 0 {
		return nil, 0, false, ErrInvalidUserID
	}
	// 校验状态
	st, err := normalizeStatus(status)
	if err != nil {
		return nil, 0, false, err
	}
	// 统一限制分页大小
	lim := normalizeLimit(limit)
	return s.repo.ListIncomingFriendRequests(userID, st, cursor, lim)
}

// ListOutgoingFriendRequests 查询我发出的好友申请。
func (s *Service) ListOutgoingFriendRequests(userID uint, status string, cursor uint, limit int) ([]model.FriendRequest, uint, bool, error) {
	// 非法用户 ID
	if userID == 0 {
		return nil, 0, false, ErrInvalidUserID
	}
	// 校验状态
	st, err := normalizeStatus(status)
	if err != nil {
		return nil, 0, false, err
	}
	// 统一限制分页大小
	lim := normalizeLimit(limit)
	return s.repo.ListOutgoingFriendRequests(userID, st, cursor, lim)
}

// SendFriendRequest 发送好友申请。
func (s *Service) SendFriendRequest(fromID, toID uint, remark string) (*model.FriendRequest, error) {
	if fromID == 0 || toID == 0 {
		return nil, ErrInvalidUserID
	}
	if fromID == toID {
		return nil, ErrCannotAddSelf
	}
	// 已是好友不重复申请
	count, err := s.repo.CountRelation(fromID, toID)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, ErrAlreadyFriends
	}
	// 拉黑检查（双向）
	if blocked, err := s.repo.IsBlocked(fromID, toID); err != nil {
		return nil, err
	} else if blocked {
		return nil, ErrBlocked
	}
	if blocked, err := s.repo.IsBlocked(toID, fromID); err != nil {
		return nil, err
	} else if blocked {
		return nil, ErrBlocked
	}
	return s.repo.CreateFriendRequest(fromID, toID, remark)
}

// ApproveFriendRequest 同意好友申请。
func (s *Service) ApproveFriendRequest(requestID, operatorUserID uint) error {
	req, err := s.repo.GetFriendRequestByID(requestID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRequestNotFound
		}
		return err
	}
	// 只有接收方可以同意
	if req.ToUserID != operatorUserID {
		return ErrRequestStateInvalid
	}
	// 仅 pending 可流转
	if req.Status != "pending" {
		return ErrRequestStateInvalid
	}
	// 再次校验拉黑
	if blocked, err := s.repo.IsBlocked(req.FromUserID, req.ToUserID); err != nil {
		return err
	} else if blocked {
		return ErrBlocked
	}
	if blocked, err := s.repo.IsBlocked(req.ToUserID, req.FromUserID); err != nil {
		return err
	} else if blocked {
		return ErrBlocked
	}
	// 建立双向好友（幂等）：如果已经是好友则不重复创建，避免交叉申请二次同意时报唯一索引冲突。
	exists, err := s.repo.CountRelation(req.FromUserID, req.ToUserID)
	if err != nil {
		return err
	}
	if exists == 0 {
		if err := s.repo.CreateBidirectional(req.FromUserID, req.ToUserID); err != nil {
			return err
		}
	}
	// 更新申请状态
	if err := s.repo.UpdateFriendRequestStatus(req.ID, "accepted"); err != nil {
		return err
	}
	s.repo.DelCache(req.FromUserID)
	s.repo.DelCache(req.ToUserID)
	return nil
}

// RejectFriendRequest 拒绝好友申请。
func (s *Service) RejectFriendRequest(requestID, operatorUserID uint) error {
	// GetFriendRequestByID 按 ID 查询好友申请。
	req, err := s.repo.GetFriendRequestByID(requestID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrRequestNotFound
		}
		return err
	}
	// 只有接收方可以拒绝
	if req.ToUserID != operatorUserID {
		return ErrRequestStateInvalid
	}
	// 仅 pending 可流转
	if req.Status != "pending" {
		return ErrRequestStateInvalid
	}
	// 更新申请状态
	return s.repo.UpdateFriendRequestStatus(req.ID, "rejected")
}

// BlockUser 拉黑用户。
func (s *Service) BlockUser(userID, blockedUserID uint) error {
	// 非法用户 ID
	if userID == 0 || blockedUserID == 0 {
		return ErrInvalidUserID
	}
	// 不能拉黑自己
	if userID == blockedUserID {
		return ErrCannotAddSelf
	}
	return s.repo.BlockUser(userID, blockedUserID)
}

// IsFriend 判断是否为好友。
func (s *Service) IsFriend(userID, targetID uint) (bool, error) {
	if userID == 0 || targetID == 0 {
		return false, ErrInvalidUserID
	}
	count, err := s.repo.CountRelation(userID, targetID)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// GetFriendRequestByID 按申请 ID 查询申请详情。
func (s *Service) GetFriendRequestByID(requestID uint) (*model.FriendRequest, error) {
	if requestID == 0 {
		return nil, ErrRequestNotFound
	}
	// 查询好友申请
	req, err := s.repo.GetFriendRequestByID(requestID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRequestNotFound
		}
		return nil, err
	}
	return req, nil
}

// normalizeStatus 校验并标准化申请状态。
func normalizeStatus(status string) (string, error) {
	// 去除空格并转换为小写
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return "", nil
	}
	switch status {
	case "pending", "accepted", "rejected", "cancelled":
		return status, nil
	default:
		return "", ErrInvalidStatus
	}
}

// normalizeLimit 统一限制分页大小。
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

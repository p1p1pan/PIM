package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	filemodel "pim/internal/file/model"
	filerepo "pim/internal/file/repo"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrInvalidUserID    = errors.New("invalid user id")
	ErrInvalidFileID    = errors.New("invalid file id")
	ErrInvalidRequest   = errors.New("invalid request")
	ErrFileNotFound     = errors.New("file not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrUploadNotReady   = errors.New("upload not ready")
)

// Service 封装文件业务逻辑。
type Service struct {
	repo          *filerepo.FileRepo
	store         ObjectStore
	groupChecker  GroupMemberChecker
	friendChecker FriendChecker
}

// ObjectStore 是文件服务依赖的对象存储抽象。
type ObjectStore interface {
	ObjectExists(ctx context.Context, objectKey string) (bool, error)
}

// GroupMemberChecker 用于通过外部服务做群成员校验，避免直接跨域查表。
type GroupMemberChecker interface {
	IsMember(ctx context.Context, groupID, userID uint) (bool, error)
}

// FriendChecker 用于校验私聊双方是否仍为好友关系。
type FriendChecker interface {
	IsFriend(ctx context.Context, userID, targetUserID uint) (bool, error)
}

// NewService 创建服务。
func NewService(repo *filerepo.FileRepo, store ObjectStore, groupChecker GroupMemberChecker, friendChecker FriendChecker) *Service {
	return &Service{repo: repo, store: store, groupChecker: groupChecker, friendChecker: friendChecker}
}

// PrepareUploadInput 申请上传入参。
type PrepareUploadInput struct {
	OwnerUserID    uint
	ClientUploadID string
	FileName       string
	FileSize       int64
	MimeType       string
	BizType        string
	PeerID         uint
	GroupID        uint
}

// PrepareUpload 创建/返回幂等上传记录。
func (s *Service) PrepareUpload(in PrepareUploadInput) (*filemodel.File, error) {
	if in.OwnerUserID == 0 || in.FileName == "" || in.FileSize <= 0 || in.MimeType == "" {
		return nil, ErrInvalidRequest
	}
	if in.BizType != "private" && in.BizType != "group" {
		return nil, ErrInvalidRequest
	}
	if in.BizType == "private" && in.PeerID == 0 {
		return nil, ErrInvalidRequest
	}
	if in.BizType == "group" && in.GroupID == 0 {
		return nil, ErrInvalidRequest
	}
	// 幂等处理
	if in.ClientUploadID != "" {
		if old, err := s.repo.GetByOwnerUploadID(in.OwnerUserID, in.ClientUploadID); err == nil {
			// 幂等命中：直接返回已存在记录，不重复创建对象元数据。
			return old, nil
		}
	}
	// 如果 client_upload_id 为空，则生成一个唯一的 client_upload_id
	if in.ClientUploadID == "" {
		in.ClientUploadID = uuid.NewString()
	}
	// 生成对象存储的 key
	objectKey := fmt.Sprintf("im/%s/%s/%d/%s_%s",
		in.BizType,
		time.Now().Format("2006/01"),
		in.OwnerUserID,
		strings.ReplaceAll(uuid.NewString(), "-", ""),
		strings.ReplaceAll(in.ClientUploadID, "-", ""),
	)
	// 创建文件元数据
	f := &filemodel.File{
		OwnerUserID:    in.OwnerUserID,
		BizType:        in.BizType,
		PeerID:         in.PeerID,
		GroupID:        in.GroupID,
		ObjectKey:      objectKey,
		FileName:       in.FileName,
		FileSize:       in.FileSize,
		MimeType:       in.MimeType,
		Status:         "pending_upload",
		ClientUploadID: in.ClientUploadID,
	}
	if err := s.repo.Create(f); err != nil {
		return nil, err
	}
	return f, nil
}

// CommitUpload 标记上传已完成并进入扫描中。
func (s *Service) CommitUpload(fileID, ownerID uint, etag string) (*filemodel.File, bool, error) {
	if fileID == 0 {
		return nil, false, ErrInvalidFileID
	}
	if ownerID == 0 {
		return nil, false, ErrInvalidUserID
	}
	current, err := s.repo.GetByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, ErrFileNotFound
		}
		return nil, false, err
	}
	if current.OwnerUserID != ownerID {
		return nil, false, ErrFileNotFound
	}
	if (current.Status == "pending_upload" || current.Status == "uploaded") && s.store != nil {
		// 在进入 scanning 前做一次对象存在性校验，避免扫描空对象。
		ok, err := s.store.ObjectExists(context.Background(), current.ObjectKey)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, ErrUploadNotReady
		}
	}
	// 标记上传已完成并进入扫描中
	f, needPublish, err := s.repo.MarkCommitted(fileID, ownerID, etag)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, ErrFileNotFound
		}
		return nil, false, err
	}
	return f, needPublish, nil
}

// GetByObjectKey 查询 object key 对应的文件元数据。
func (s *Service) GetByObjectKey(objectKey string) (*filemodel.File, error) {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return nil, ErrInvalidRequest
	}
	f, err := s.repo.GetByObjectKey(objectKey)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return f, nil
}

// GetFile 查询文件详情并做权限检查。
func (s *Service) GetFile(fileID, operatorUserID uint) (*filemodel.File, error) {
	if fileID == 0 {
		return nil, ErrInvalidFileID
	}
	if operatorUserID == 0 {
		return nil, ErrInvalidUserID
	}
	// 查询文件详情
	f, err := s.repo.GetByID(fileID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	// 权限检查：
	// 1) owner 永远可读；
	// 2) private 文件允许会话对端（peer_id）读取；
	// 3) group 文件允许群成员读取。
	if f.OwnerUserID != operatorUserID {
		allowed := false
		switch f.BizType {
		case "private":
			if f.PeerID != operatorUserID {
				allowed = false
				break
			}
			if s.friendChecker == nil {
				return nil, errors.New("friend checker not configured")
			}
			ok, checkErr := s.friendChecker.IsFriend(context.Background(), f.OwnerUserID, operatorUserID)
			if checkErr != nil {
				return nil, checkErr
			}
			// 私聊文件要求双方仍为好友，避免历史链接越权访问。
			allowed = ok
		case "group":
			if s.groupChecker == nil {
				return nil, errors.New("group member checker not configured")
			}
			ok, checkErr := s.groupChecker.IsMember(context.Background(), f.GroupID, operatorUserID)
			if checkErr != nil {
				return nil, checkErr
			}
			// 群文件权限以“当前是否仍在群内”为准。
			allowed = ok
		}
		if !allowed {
			return nil, ErrPermissionDenied
		}
	}
	return f, nil
}

// ApplyScanResult 应用扫描结果。
func (s *Service) ApplyScanResult(fileID uint, result, reason string) (*filemodel.File, error) {
	if fileID == 0 {
		return nil, ErrInvalidFileID
	}
	// 应用扫描结果
	return s.repo.ApplyScanResult(fileID, result, reason)
}

// MarkScanFailed 标记扫描最终失败（超过重试次数）。
func (s *Service) MarkScanFailed(fileID uint, reason string) (*filemodel.File, error) {
	if fileID == 0 {
		return nil, ErrInvalidFileID
	}
	if strings.TrimSpace(reason) == "" {
		reason = "scan_failed"
	}
	return s.repo.MarkScanFailed(fileID, reason)
}

// ExpirePendingUploads 清理超时未上传文件。
func (s *Service) ExpirePendingUploads(timeout time.Duration) (int64, error) {
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	before := time.Now().Add(-timeout)
	return s.repo.ExpirePendingUploads(before)
}

// UpdateScanRetry 记录扫描任务重试状态。
func (s *Service) UpdateScanRetry(fileID uint, retry int, lastErr string, nextRetryAt time.Time) error {
	if fileID == 0 {
		return ErrInvalidFileID
	}
	return s.repo.UpdateScanRetry(fileID, retry, lastErr, nextRetryAt)
}

// ListDLQTasks 查询死信任务。
func (s *Service) ListDLQTasks(limit, offset int) ([]filemodel.FileScanTask, error) {
	return s.repo.ListDLQTasks(limit, offset)
}

// ReplayDLQTask 重放死信任务。
func (s *Service) ReplayDLQTask(fileID uint) (*filemodel.FileScanTask, error) {
	if fileID == 0 {
		return nil, ErrInvalidFileID
	}
	return s.repo.ReplayDLQTask(fileID)
}

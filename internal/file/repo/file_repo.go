package repo

import (
	"errors"
	"time"

	filemodel "pim/internal/file/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FileRepo 封装文件元数据访问。
type FileRepo struct {
	db *gorm.DB
}

// ExpirePendingUploads 将超时未提交上传的文件标记为 rejected。
func (r *FileRepo) ExpirePendingUploads(before time.Time) (int64, error) {
	tx := r.db.Model(&filemodel.File{}).
		Where("status = ? AND created_at < ?", "pending_upload", before).
		Updates(map[string]interface{}{
			"status":        "rejected",
			"reject_reason": "upload_timeout",
		})
	return tx.RowsAffected, tx.Error
}

// NewFileRepo 创建仓储。
func NewFileRepo(db *gorm.DB) *FileRepo {
	return &FileRepo{db: db}
}

// GetByOwnerUploadID 按 owner + client_upload_id 查询幂等记录。
func (r *FileRepo) GetByOwnerUploadID(ownerID uint, uploadID string) (*filemodel.File, error) {
	var f filemodel.File
	if err := r.db.Where("owner_user_id = ? AND client_upload_id = ?", ownerID, uploadID).First(&f).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// Create 创建文件元数据。
func (r *FileRepo) Create(f *filemodel.File) error {
	return r.db.Create(f).Error
}

// GetByID 查询文件。
func (r *FileRepo) GetByID(fileID uint) (*filemodel.File, error) {
	var f filemodel.File
	if err := r.db.First(&f, fileID).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// GetByObjectKey 按对象键查询文件元数据。
func (r *FileRepo) GetByObjectKey(objectKey string) (*filemodel.File, error) {
	var f filemodel.File
	if err := r.db.Where("object_key = ?", objectKey).First(&f).Error; err != nil {
		return nil, err
	}
	return &f, nil
}

// IsGroupMember 判断用户是否为指定群成员。
func (r *FileRepo) IsGroupMember(groupID, userID uint) (bool, error) {
	if groupID == 0 || userID == 0 {
		return false, nil
	}
	var cnt int64
	if err := r.db.Table("group_members").
		Where("group_id = ? AND user_id = ?", groupID, userID).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// MarkCommitted 更新上传完成状态并写扫描任务（幂等）。
func (r *FileRepo) MarkCommitted(fileID, ownerID uint, etag string) (*filemodel.File, bool, error) {
	var out filemodel.File
	needPublish := false
	// 事务内锁定文件记录，确保状态流转和扫描任务写入的一致性。
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var f filemodel.File
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&f, fileID).Error; err != nil {
			return err
		}
		if f.OwnerUserID != ownerID {
			return gorm.ErrRecordNotFound
		}
		if f.Status == "pending_upload" || f.Status == "uploaded" {
			// commit 幂等：仅首次进入 scanning 时投递扫描任务。
			f.Status = "scanning"
			f.ETag = etag
			if err := tx.Save(&f).Error; err != nil {
				return err
			}
			needPublish = true
			task := filemodel.FileScanTask{
				FileID: f.ID,
				Status: "pending",
			}
			if err := tx.Create(&task).Error; err != nil {
				return err
			}
		}
		out = f
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &out, needPublish, nil
}

// UpdateScanRetry 更新扫描任务重试状态。
func (r *FileRepo) UpdateScanRetry(fileID uint, retry int, lastErr string, nextRetryAt time.Time) error {
	return r.db.Model(&filemodel.FileScanTask{}).
		Where("file_id = ? AND status = ?", fileID, "pending").
		Updates(map[string]interface{}{
			"retry_count":   retry,
			"last_error":    lastErr,
			"next_retry_at": nextRetryAt,
		}).Error
}

// ApplyScanResult 写入扫描结果。
func (r *FileRepo) ApplyScanResult(fileID uint, result, reason string) (*filemodel.File, error) {
	var out filemodel.File
	// 扫描结果写回与任务完成标记在同一事务中执行。
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var f filemodel.File
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&f, fileID).Error; err != nil {
			return err
		}
		switch result {
		case "ok":
			f.Status = "ok"
			f.RejectReason = ""
		case "rejected":
			f.Status = "rejected"
			f.RejectReason = reason
		default:
			return errors.New("invalid scan result")
		}
		if err := tx.Save(&f).Error; err != nil {
			return err
		}
		_ = tx.Model(&filemodel.FileScanTask{}).
			Where("file_id = ? AND status = ?", fileID, "pending").
			Updates(map[string]interface{}{"status": "done", "result": result}).Error
		out = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkScanFailed 将扫描失败且超过重试次数的文件标记为 rejected。
func (r *FileRepo) MarkScanFailed(fileID uint, reason string) (*filemodel.File, error) {
	var out filemodel.File
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var f filemodel.File
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&f, fileID).Error; err != nil {
			return err
		}
		f.Status = "rejected"
		f.RejectReason = reason
		if err := tx.Save(&f).Error; err != nil {
			return err
		}
		now := time.Now()
		_ = tx.Model(&filemodel.FileScanTask{}).
			Where("file_id = ? AND status = ?", fileID, "pending").
			Updates(map[string]interface{}{
				"status":           "dead_letter",
				"result":           "rejected",
				"last_error":       reason,
				"dead_lettered_at": now,
			}).Error
		out = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListDLQTasks 查询死信任务列表。
func (r *FileRepo) ListDLQTasks(limit, offset int) ([]filemodel.FileScanTask, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var tasks []filemodel.FileScanTask
	err := r.db.Where("status = ?", "dead_letter").
		Order("updated_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&tasks).Error
	return tasks, err
}

// ReplayDLQTask 将死信任务恢复为 pending，供重放。
func (r *FileRepo) ReplayDLQTask(fileID uint) (*filemodel.FileScanTask, error) {
	var out filemodel.FileScanTask
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var task filemodel.FileScanTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("file_id = ? AND status = ?", fileID, "dead_letter").
			Order("updated_at DESC").
			First(&task).Error; err != nil {
			return err
		}
		task.Status = "pending"
		task.Result = ""
		task.RetryCount = 0
		task.LastError = ""
		task.NextRetryAt = nil
		task.DeadLetteredAt = nil
		if err := tx.Save(&task).Error; err != nil {
			return err
		}
		_ = tx.Model(&filemodel.File{}).
			Where("id = ?", fileID).
			Updates(map[string]interface{}{
				"status":        "scanning",
				"reject_reason": "",
			}).Error
		out = task
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

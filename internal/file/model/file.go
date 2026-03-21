package model

import "time"

// File 表示文件元数据。
type File struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	OwnerUserID    uint      `gorm:"not null;index" json:"owner_user_id"`
	BizType        string    `gorm:"type:varchar(16);not null;index" json:"biz_type"` // private/group
	PeerID         uint      `gorm:"not null;default:0;index" json:"peer_id"`
	GroupID        uint      `gorm:"not null;default:0;index" json:"group_id"`
	ObjectKey      string    `gorm:"type:varchar(255);not null;uniqueIndex" json:"object_key"`
	FileName       string    `gorm:"type:varchar(255);not null" json:"file_name"`
	FileSize       int64     `gorm:"not null" json:"file_size"`
	MimeType       string    `gorm:"type:varchar(127);not null" json:"mime_type"`
	Status         string    `gorm:"type:varchar(32);not null;index" json:"status"`
	RejectReason   string    `gorm:"type:text;not null;default:''" json:"reject_reason"`
	ClientUploadID string    `gorm:"type:varchar(64);not null;default:'';index:idx_owner_uploadid,unique" json:"client_upload_id"`
	ETag           string    `gorm:"type:varchar(127);not null;default:''" json:"etag"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// FileScanTask 表示文件扫描任务。
type FileScanTask struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	FileID         uint       `gorm:"not null;index" json:"file_id"`
	Status         string     `gorm:"type:varchar(32);not null;index" json:"status"` // pending/done/dead_letter
	Result         string     `gorm:"type:varchar(32);not null;default:''" json:"result"`
	RetryCount     int        `gorm:"not null;default:0" json:"retry_count"`
	LastError      string     `gorm:"type:text;not null;default:''" json:"last_error"`
	NextRetryAt    *time.Time `json:"next_retry_at"`
	DeadLetteredAt *time.Time `json:"dead_lettered_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// FileScanEvent 是写入 file-scan topic 的事件载荷。
type FileScanEvent struct {
	TraceID string `json:"trace_id"`
	FileID  uint   `json:"file_id"`
	Retry   int    `json:"retry"`
}

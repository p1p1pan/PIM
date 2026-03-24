package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbfile "pim/internal/file/pb"
	gatewaymodel "pim/internal/gateway/model"
)

// handleFilePrepare 申请文件上传。
func (s *HTTPServer) handleFilePrepare(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	var req gatewaymodel.FilePrepareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	// Prepare 阶段只生成上传任务与预签名信息，不落最终可下载状态。
	resp, err := s.fileClient.PrepareUpload(ctxWithTrace(c), &pbfile.PrepareUploadRequest{
		OwnerUserId:    uint64(userID),
		ClientUploadId: req.ClientUploadID,
		FileName:       req.FileName,
		FileSize:       req.FileSize,
		MimeType:       req.MimeType,
		BizType:        req.BizType,
		PeerId:         req.PeerID,
		GroupId:        req.GroupID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.InvalidArgument {
			c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 记录业务日志，后续可按 client_upload_id 追踪文件链路。
	s.emitBizLog(c, "file prepare success", req.ClientUploadID, nil)
	c.JSON(http.StatusOK, gin.H{"file": mapPBFile(resp.GetFile())})
}

// handleFileCommit 确认文件上传完成。
func (s *HTTPServer) handleFileCommit(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	fileID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || fileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
		return
	}
	var req gatewaymodel.FileCommitRequest
	_ = c.ShouldBindJSON(&req)
	// Commit 阶段确认对象已上传，触发扫描链路。
	resp, err := s.fileClient.CommitUpload(ctxWithTrace(c), &pbfile.CommitUploadRequest{
		FileId:      fileID,
		OwnerUserId: uint64(userID),
		Etag:        req.ETag,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.FailedPrecondition:
				c.JSON(http.StatusConflict, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 这里的 event_id 使用 file_id，便于从日志回查具体文件对象。
	s.emitBizLog(c, "file commit success", strconv.FormatUint(fileID, 10), nil)
	c.JSON(http.StatusOK, gin.H{"file": mapPBFile(resp.GetFile())})
}

// handleFileGet 查询文件信息/下载地址。
func (s *HTTPServer) handleFileGet(c *gin.Context) {
	userID, ok := requireUserID(c)
	if !ok {
		return
	}
	fileID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || fileID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file id"})
		return
	}
	// GetFile 内部会做权限校验（owner/friend/group member）。
	resp, err := s.fileClient.GetFile(ctxWithTrace(c), &pbfile.GetFileRequest{
		FileId:         fileID,
		OperatorUserId: uint64(userID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.InvalidArgument:
				c.JSON(http.StatusBadRequest, gin.H{"error": st.Message()})
				return
			case codes.NotFound:
				c.JSON(http.StatusNotFound, gin.H{"error": st.Message()})
				return
			case codes.PermissionDenied:
				c.JSON(http.StatusForbidden, gin.H{"error": st.Message()})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.emitBizLog(c, "file get success", strconv.FormatUint(fileID, 10), nil)
	c.JSON(http.StatusOK, gin.H{"file": mapPBFile(resp.GetFile())})
}

func mapPBFile(f *pbfile.FileItem) gin.H {
	if f == nil {
		return gin.H{}
	}
	return gin.H{
		"id":            f.GetId(),
		"owner_user_id": f.GetOwnerUserId(),
		"biz_type":      f.GetBizType(),
		"peer_id":       f.GetPeerId(),
		"group_id":      f.GetGroupId(),
		"object_key":    f.GetObjectKey(),
		"file_name":     f.GetFileName(),
		"file_size":     f.GetFileSize(),
		"mime_type":     f.GetMimeType(),
		"status":        f.GetStatus(),
		"reject_reason": f.GetRejectReason(),
		"upload_url":    f.GetUploadUrl(),
		"download_url":  f.GetDownloadUrl(),
		"created_at":    f.GetCreatedAt(),
		"updated_at":    f.GetUpdatedAt(),
	}
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"time"

	filemodel "pim/internal/file/model"
	pbfile "pim/internal/file/pb"
	fileservice "pim/internal/file/service"
	"pim/internal/kit/mq/kafka"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCFileServer 实现 File gRPC。
type GRPCFileServer struct {
	pbfile.UnimplementedFileServiceServer
	svc      *fileservice.Service
	producer *kafka.Producer
	storage  URLSigner
}

// URLSigner 定义预签名 URL 能力。
type URLSigner interface {
	PresignUploadURL(ctx context.Context, objectKey string, ttl time.Duration) (*url.URL, error)
	PresignDownloadURL(ctx context.Context, objectKey string, ttl time.Duration) (*url.URL, error)
}

// NewGRPCFileServer 创建 gRPC File Server。
func NewGRPCFileServer(svc *fileservice.Service, producer *kafka.Producer, storage URLSigner) *GRPCFileServer {
	return &GRPCFileServer{svc: svc, producer: producer, storage: storage}
}

func (s *GRPCFileServer) PrepareUpload(ctx context.Context, req *pbfile.PrepareUploadRequest) (*pbfile.PrepareUploadResponse, error) {
	// 使用service中的PrepareUpload方法准备上传
	f, err := s.svc.PrepareUpload(fileservice.PrepareUploadInput{
		OwnerUserID:    uint(req.GetOwnerUserId()),
		ClientUploadID: req.GetClientUploadId(),
		FileName:       req.GetFileName(),
		FileSize:       req.GetFileSize(),
		MimeType:       req.GetMimeType(),
		BizType:        req.GetBizType(),
		PeerID:         uint(req.GetPeerId()),
		GroupID:        uint(req.GetGroupId()),
	})
	if err != nil {
		if errors.Is(err, fileservice.ErrInvalidRequest) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "prepare upload failed: %v", err)
	}
	// 返回文件元数据
	return &pbfile.PrepareUploadResponse{File: s.toPBWithSignedURL(ctx, f)}, nil
}

func (s *GRPCFileServer) CommitUpload(ctx context.Context, req *pbfile.CommitUploadRequest) (*pbfile.CommitUploadResponse, error) {
	// 使用service中的CommitUpload方法提交上传
	f, needPublish, err := s.svc.CommitUpload(uint(req.GetFileId()), uint(req.GetOwnerUserId()), req.GetEtag())
	if err != nil {
		switch {
		case errors.Is(err, fileservice.ErrInvalidFileID), errors.Is(err, fileservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, fileservice.ErrFileNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, fileservice.ErrUploadNotReady):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "commit upload failed: %v", err)
		}
	}
	// 如果需要发布扫描事件，则发布扫描事件
	if needPublish {
		// 仅在首次 commit 成功进入 scanning 时投递 file-scan 事件。
		evt := filemodel.FileScanEvent{TraceID: fmt.Sprintf("scan-%d", time.Now().UnixNano()), FileID: f.ID}
		data, _ := json.Marshal(evt)
		if err := s.producer.SendMessage(ctx, "file-scan", "", data); err != nil {
			log.Printf("file.CommitUpload send file-scan failed: %v", err)
		}
	}
	return &pbfile.CommitUploadResponse{File: s.toPBWithSignedURL(ctx, f)}, nil
}

func (s *GRPCFileServer) GetFile(ctx context.Context, req *pbfile.GetFileRequest) (*pbfile.GetFileResponse, error) {
	// 使用service中的GetFile方法获取文件
	f, err := s.svc.GetFile(uint(req.GetFileId()), uint(req.GetOperatorUserId()))
	if err != nil {
		switch {
		case errors.Is(err, fileservice.ErrInvalidFileID), errors.Is(err, fileservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, fileservice.ErrFileNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, fileservice.ErrPermissionDenied):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "get file failed: %v", err)
		}
	}
	// 返回文件元数据
	return &pbfile.GetFileResponse{File: s.toPBWithSignedURL(ctx, f)}, nil
}

func (s *GRPCFileServer) ApplyScanResult(ctx context.Context, req *pbfile.ApplyScanResultRequest) (*pbfile.ApplyScanResultResponse, error) {
	// 使用service中的ApplyScanResult方法应用扫描结果
	f, err := s.svc.ApplyScanResult(uint(req.GetFileId()), req.GetResult(), req.GetReason())
	if err != nil {
		// 返回错误
		return nil, status.Errorf(codes.Internal, "apply scan result failed: %v", err)
	}
	// 返回文件元数据
	return &pbfile.ApplyScanResultResponse{File: s.toPBWithSignedURL(ctx, f)}, nil
}

func (s *GRPCFileServer) toPBWithSignedURL(ctx context.Context, f *filemodel.File) *pbfile.FileItem {
	if f == nil {
		return nil
	}
	uploadURL := ""
	if s.storage != nil && (f.Status == "pending_upload" || f.Status == "uploaded") {
		if u, err := s.storage.PresignUploadURL(ctx, f.ObjectKey, 15*time.Minute); err == nil {
			uploadURL = u.String()
		}
	}
	downloadURL := ""
	if s.storage != nil && f.Status == "ok" {
		if u, err := s.storage.PresignDownloadURL(ctx, f.ObjectKey, 5*time.Minute); err == nil {
			downloadURL = u.String()
		}
	}
	return &pbfile.FileItem{
		Id:           uint64(f.ID),
		OwnerUserId:  uint64(f.OwnerUserID),
		BizType:      f.BizType,
		PeerId:       uint64(f.PeerID),
		GroupId:      uint64(f.GroupID),
		ObjectKey:    f.ObjectKey,
		FileName:     f.FileName,
		FileSize:     f.FileSize,
		MimeType:     f.MimeType,
		Status:       f.Status,
		RejectReason: f.RejectReason,
		UploadUrl:    uploadURL,
		DownloadUrl:  downloadURL,
		CreatedAt:    f.CreatedAt.Unix(),
		UpdatedAt:    f.UpdatedAt.Unix(),
	}
}

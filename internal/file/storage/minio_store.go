package storage

import (
	"context"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Store 封装对象存储能力。
type Store struct {
	client *minio.Client
	bucket string
}

// NewStore 创建 MinIO 存储客户端并确保 bucket 存在。
func NewStore(ctx context.Context, endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Store, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	ok, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}
	return &Store{client: client, bucket: bucket}, nil
}

// PresignUploadURL 生成 PUT 上传预签名 URL。
func (s *Store) PresignUploadURL(ctx context.Context, objectKey string, ttl time.Duration) (*url.URL, error) {
	return s.client.PresignedPutObject(ctx, s.bucket, objectKey, ttl)
}

// PresignDownloadURL 生成 GET 下载预签名 URL。
func (s *Store) PresignDownloadURL(ctx context.Context, objectKey string, ttl time.Duration) (*url.URL, error) {
	return s.client.PresignedGetObject(ctx, s.bucket, objectKey, ttl, nil)
}

// ObjectExists 判断对象是否已存在。
func (s *Store) ObjectExists(ctx context.Context, objectKey string) (bool, error) {
	_, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.StatusCode == 404 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

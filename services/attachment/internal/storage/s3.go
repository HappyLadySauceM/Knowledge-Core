package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type S3 struct {
	internal, public       *minio.Client
	core                   *minio.Core
	bucket, region         string
	uploadTTL, downloadTTL time.Duration
}

func Open(ctx context.Context, cfg config.ObjectStorageOptions) (*S3, error) {
	if ctx == nil {
		return nil, errors.New("open attachment storage: context is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	makeClient := func(endpoint string, secure bool) (*minio.Client, error) {
		return minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: secure, Region: cfg.Region, BucketLookup: minio.BucketLookupPath, MaxRetries: 3})
	}
	internal, err := makeClient(cfg.Endpoint, cfg.Secure)
	if err != nil {
		return nil, err
	}
	public, err := makeClient(cfg.PublicEndpoint, cfg.PublicSecure)
	if err != nil {
		return nil, err
	}
	exists, err := internal.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check attachment bucket: %w", err)
	}
	if !exists && cfg.AutoCreateBucket {
		if err = internal.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, err
		}
	} else if !exists {
		return nil, fmt.Errorf("attachment bucket %q does not exist", cfg.Bucket)
	}
	core, err := minio.NewCore(cfg.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: cfg.Secure, Region: cfg.Region, BucketLookup: minio.BucketLookupPath, MaxRetries: 3})
	if err != nil {
		return nil, err
	}
	return &S3{internal: internal, public: public, core: core, bucket: cfg.Bucket, region: cfg.Region, uploadTTL: cfg.UploadTTL, downloadTTL: cfg.DownloadTTL}, nil
}
func (s *S3) Ping(ctx context.Context) error {
	if s == nil || s.internal == nil {
		return errors.New("attachment storage is nil")
	}
	ok, err := s.internal.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("attachment bucket unavailable")
	}
	return nil
}
func (s *S3) StartMultipart(ctx context.Context, key, mediaType string) (string, error) {
	u, err := s.core.NewMultipartUpload(ctx, s.bucket, key, minio.PutObjectOptions{ContentType: mediaType})
	if err != nil {
		return "", fmt.Errorf("start multipart upload: %w", err)
	}
	return u, nil
}
func (s *S3) PresignPart(ctx context.Context, key, uploadID string, part int) (string, time.Time, error) {
	if part <= 0 {
		return "", time.Time{}, errors.New("part number is invalid")
	}
	params := url.Values{"uploadId": []string{uploadID}, "partNumber": []string{strconv.Itoa(part)}}
	ttl := s.uploadTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	u, err := s.public.Presign(ctx, "PUT", s.bucket, key, ttl, params)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign attachment part: %w", err)
	}
	return u.String(), time.Now().UTC().Add(ttl), nil
}
func (s *S3) CompleteMultipart(ctx context.Context, key, uploadID string, parts []minio.CompletePart) error {
	if _, err := s.core.CompleteMultipartUpload(ctx, s.bucket, key, uploadID, parts, minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	return nil
}
func (s *S3) AbortMultipart(ctx context.Context, key, uploadID string) error {
	return s.core.AbortMultipartUpload(ctx, s.bucket, key, uploadID)
}
func (s *S3) OpenObject(ctx context.Context, key string) (*minio.Object, error) {
	obj, err := s.internal.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}
func (s *S3) PresignDownload(ctx context.Context, key string) (string, time.Time, error) {
	u, err := s.public.PresignedGetObject(ctx, s.bucket, key, s.downloadTTL, nil)
	if err != nil {
		return "", time.Time{}, err
	}
	return u.String(), time.Now().UTC().Add(s.downloadTTL), nil
}
func (s *S3) Remove(ctx context.Context, key string) error {
	return s.internal.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

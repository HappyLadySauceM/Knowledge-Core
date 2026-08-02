package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var ErrObjectMismatch = errors.New("object storage: uploaded object does not match declaration")

type S3 struct {
	client      *minio.Client
	bucket      string
	region      string
	uploadTTL   time.Duration
	downloadTTL time.Duration
}

func Open(ctx context.Context, options config.ObjectStorageOptions) (*S3, error) {
	if ctx == nil {
		return nil, errors.New("open object storage: context is required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("open object storage: invalid options: %w", err)
	}
	client, err := minio.New(options.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(options.AccessKey, options.SecretKey, ""),
		Secure: options.Secure, Region: options.Region, BucketLookup: minio.BucketLookupPath,
		MaxRetries: 3,
	})
	if err != nil {
		return nil, fmt.Errorf("create S3 client: %w", err)
	}
	exists, err := client.BucketExists(ctx, options.Bucket)
	if err != nil {
		return nil, fmt.Errorf("check S3 bucket: %w", err)
	}
	if !exists && options.AutoCreateBucket {
		if err := client.MakeBucket(ctx, options.Bucket, minio.MakeBucketOptions{Region: options.Region}); err != nil {
			return nil, fmt.Errorf("create S3 bucket: %w", err)
		}
		exists = true
	}
	if !exists {
		return nil, fmt.Errorf("open object storage: bucket %q does not exist", options.Bucket)
	}
	return &S3{
		client: client, bucket: options.Bucket, region: options.Region,
		uploadTTL: options.UploadTTL, downloadTTL: options.DownloadTTL,
	}, nil
}

func (s *S3) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return errors.New("ping object storage: client is nil")
	}
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("ping object storage: %w", err)
	}
	if !exists {
		return errors.New("ping object storage: bucket is unavailable")
	}
	return nil
}

func (s *S3) PresignUpload(ctx context.Context, objectKey, mediaType, checksum string, size int64, expiresAt time.Time) (domain.UploadTarget, error) {
	if s == nil || s.client == nil {
		return domain.UploadTarget{}, errors.New("presign object upload: client is nil")
	}
	headers := http.Header{}
	headers.Set("Content-Type", mediaType)
	headers.Set("X-Amz-Meta-Sha256", checksum)
	headers.Set("X-Amz-Meta-Size", strconv.FormatInt(size, 10))
	expires := time.Until(expiresAt)
	if expires > s.uploadTTL {
		expires = s.uploadTTL
	}
	if expires < time.Second {
		return domain.UploadTarget{}, ErrObjectMismatch
	}
	u, err := s.client.PresignHeader(ctx, http.MethodPut, s.bucket, objectKey, expires, nil, headers)
	if err != nil {
		return domain.UploadTarget{}, fmt.Errorf("presign object upload: %w", err)
	}
	return domain.UploadTarget{
		URL: u.String(), ExpiresAt: time.Now().UTC().Add(expires),
		RequiredHeaders: map[string]string{
			"Content-Type": mediaType, "X-Amz-Meta-Sha256": checksum,
			"X-Amz-Meta-Size": strconv.FormatInt(size, 10),
		},
	}, nil
}

func (s *S3) VerifyUpload(ctx context.Context, objectKey, expectedChecksum string, expectedSize int64) error {
	if s == nil || s.client == nil {
		return errors.New("verify object upload: client is nil")
	}
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return fmt.Errorf("stat uploaded object: %w", err)
	}
	if info.Size != expectedSize || metadataValue(info.UserMetadata, "sha256") != expectedChecksum || metadataValue(info.UserMetadata, "size") != strconv.FormatInt(expectedSize, 10) {
		return fmt.Errorf("verify object upload metadata: %w", ErrObjectMismatch)
	}
	reader, err := s.OpenObject(ctx, objectKey)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(reader, expectedSize+1))
	if err != nil {
		return fmt.Errorf("hash uploaded object: %w", err)
	}
	if written != expectedSize || hex.EncodeToString(hash.Sum(nil)) != expectedChecksum {
		return fmt.Errorf("verify object upload content: %w", ErrObjectMismatch)
	}
	return nil
}

func metadataValue(values minio.StringMap, key string) string {
	for name, value := range values {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}

func (s *S3) OpenObject(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("open object: client is nil")
	}
	object, err := s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}
	return object, nil
}

func (s *S3) PresignDownload(ctx context.Context, objectKey, filename, mediaType string) (string, time.Time, error) {
	if s == nil || s.client == nil {
		return "", time.Time{}, errors.New("presign object download: client is nil")
	}
	parameters := url.Values{}
	parameters.Set("response-content-type", mediaType)
	parameters.Set("response-content-disposition", fmt.Sprintf("inline; filename*=UTF-8''%s", url.PathEscape(filename)))
	u, err := s.client.PresignedGetObject(ctx, s.bucket, objectKey, s.downloadTTL, parameters)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign object download: %w", err)
	}
	return u.String(), time.Now().UTC().Add(s.downloadTTL), nil
}

func (s *S3) RemoveObject(ctx context.Context, objectKey string) error {
	if s == nil || s.client == nil {
		return errors.New("remove object: client is nil")
	}
	if err := s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object: %w", err)
	}
	return nil
}

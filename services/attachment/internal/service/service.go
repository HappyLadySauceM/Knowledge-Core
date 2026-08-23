package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/domain"
	attachmenterrors "github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/scanner"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/storage"
	"github.com/minio/minio-go/v7"
	"gorm.io/gorm"
)

type Service struct {
	repo      *repository.Store
	objects   *storage.S3
	scanner   *scanner.ClamAV
	uploadTTL time.Duration
	partSize  int64
}

func New(repo *repository.Store, objects *storage.S3, scan *scanner.ClamAV, ttl time.Duration) (*Service, error) {
	if repo == nil || objects == nil || scan == nil || ttl <= 0 {
		return nil, errors.New("attachment service dependencies are required")
	}
	return &Service{repo: repo, objects: objects, scanner: scan, uploadTTL: ttl, partSize: domain.PartSize}, nil
}
func (s *Service) Create(ctx context.Context, owner int64, req *attachmentv1.CreateAttachmentRequest) (*attachmentv1.AttachmentUpload, error) {
	if owner <= 0 || req == nil {
		return nil, attachmenterrors.InvalidInput.New()
	}
	category, err := domain.Validate(req.Filename, req.MediaType, req.SizeBytes)
	if err != nil {
		return nil, attachmenterrors.InvalidInput.Wrap(err)
	}
	parts := int32(math.Ceil(float64(req.SizeBytes) / float64(s.partSize)))
	if parts < 1 || parts > domain.MaxParts {
		return nil, attachmenterrors.InvalidInput.New()
	}
	idempotencyKey := req.GetIdempotencyKey()
	if err := domain.ValidateIdempotencyKey(idempotencyKey); err != nil {
		return nil, attachmenterrors.InvalidInput.Wrap(err)
	}
	requestHash, err := hashCreateRequest(strings.TrimSpace(req.Filename), strings.ToLower(strings.TrimSpace(req.MediaType)), req.SizeBytes)
	if err != nil {
		return nil, attachmenterrors.Internal.Wrap(err)
	}
	id, err := domain.NewID()
	if err != nil {
		return nil, attachmenterrors.Internal.Wrap(err)
	}
	key := domain.ObjectKey(id)
	uploadID, err := s.objects.StartMultipart(ctx, key, req.MediaType)
	if err != nil {
		return nil, attachmenterrors.Unavailable.Wrap(err)
	}
	now := time.Now().UTC()
	record := &domain.Attachment{ID: id, OwnerID: owner, Filename: strings.TrimSpace(req.Filename), MediaType: strings.ToLower(strings.TrimSpace(req.MediaType)), Category: category, SizeBytes: req.SizeBytes, ObjectKey: key, UploadID: uploadID, Status: domain.StatusPendingUpload, PartSize: s.partSize, PartCount: parts, CreatedAt: now, UpdatedAt: now}
	stored, err := s.repo.Create(ctx, record, repository.Idempotency{OwnerID: owner, Key: idempotencyKey, RequestHash: requestHash})
	if err != nil {
		_ = s.objects.AbortMultipart(ctx, key, uploadID)
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			return nil, attachmenterrors.Conflict.New()
		}
		if errors.Is(err, repository.ErrConflict) {
			return nil, attachmenterrors.QuotaExceeded.New()
		}
		return nil, attachmenterrors.Internal.Wrap(err)
	}
	if stored.ID != record.ID {
		if stored.Status != domain.StatusPendingUpload {
			_ = s.objects.AbortMultipart(ctx, key, uploadID)
			return nil, attachmenterrors.Conflict.New()
		}
		if abortErr := s.objects.AbortMultipart(ctx, key, uploadID); abortErr != nil {
			return nil, attachmenterrors.Unavailable.Wrap(abortErr)
		}
		record = stored
	}
	return s.uploadResult(ctx, record, now.Add(s.uploadTTL))
}

func hashCreateRequest(filename, mediaType string, size int64) (string, error) {
	payload, err := json.Marshal(struct {
		Filename  string `json:"filename"`
		MediaType string `json:"media_type"`
		SizeBytes int64  `json:"size_bytes"`
	}{filename, mediaType, size})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Service) uploadResult(ctx context.Context, record *domain.Attachment, expiresAt time.Time) (*attachmentv1.AttachmentUpload, error) {
	if record == nil || record.UploadID == "" || record.PartCount <= 0 {
		return nil, attachmenterrors.Conflict.New()
	}
	result := &attachmentv1.AttachmentUpload{Attachment: toTransport(record), UploadId: record.UploadID, ExpiresAt: expiresAt.Format(time.RFC3339)}
	result.Parts = make([]*attachmentv1.UploadPart, 0, record.PartCount)
	for i := int32(1); i <= record.PartCount; i++ {
		u, expires, err := s.objects.PresignPart(ctx, record.ObjectKey, record.UploadID, int(i))
		if err != nil {
			return nil, attachmenterrors.Unavailable.Wrap(err)
		}
		result.Parts = append(result.Parts, &attachmentv1.UploadPart{PartNumber: i, Url: u, ExpiresAt: expires.Format(time.RFC3339)})
	}
	return result, nil
}
func (s *Service) Complete(ctx context.Context, owner int64, req *attachmentv1.CompleteAttachmentRequest) (*attachmentv1.Attachment, error) {
	if owner <= 0 || req == nil || req.AttachmentId == "" || req.UploadId == "" {
		return nil, attachmenterrors.InvalidInput.New()
	}
	record, err := s.repo.Get(ctx, req.AttachmentId, owner)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if record.Status != domain.StatusPendingUpload || record.UploadID != req.UploadId || len(req.Parts) != int(record.PartCount) {
		return nil, attachmenterrors.Conflict.New()
	}
	parts := make([]minio.CompletePart, 0, len(req.Parts))
	seen := make(map[int32]bool, len(req.Parts))
	for _, part := range req.Parts {
		if part == nil || part.PartNumber < 1 || part.PartNumber > record.PartCount || seen[part.PartNumber] || strings.TrimSpace(part.Etag) == "" {
			return nil, attachmenterrors.InvalidInput.New()
		}
		seen[part.PartNumber] = true
		parts = append(parts, minio.CompletePart{PartNumber: int(part.PartNumber), ETag: strings.Trim(part.Etag, "\" ")})
	}
	for i := int32(1); i <= record.PartCount; i++ {
		if !seen[i] {
			return nil, attachmenterrors.InvalidInput.New()
		}
	}
	if err := s.objects.CompleteMultipart(ctx, record.ObjectKey, record.UploadID, parts); err != nil {
		return nil, attachmenterrors.Conflict.Wrap(err)
	}
	if err := s.repo.MarkScanning(ctx, record.ID, owner); err != nil {
		return nil, mapRepositoryError(err)
	}
	record.Status = domain.StatusScanning
	return toTransport(record), nil
}
func (s *Service) List(ctx context.Context, owner int64, req *attachmentv1.ListAttachmentsRequest) (*attachmentv1.AttachmentList, error) {
	if owner <= 0 || req == nil {
		return nil, attachmenterrors.InvalidInput.New()
	}
	rows, err := s.repo.List(ctx, owner, req.GetStatus(), req.GetCategory(), int(req.GetLimit()))
	if err != nil {
		return nil, attachmenterrors.Internal.Wrap(err)
	}
	out := &attachmentv1.AttachmentList{Items: make([]*attachmentv1.Attachment, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, toTransport(row))
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, owner int64, id string) (*attachmentv1.Attachment, error) {
	if owner <= 0 || strings.TrimSpace(id) == "" {
		return nil, attachmenterrors.InvalidInput.New()
	}
	row, err := s.repo.Get(ctx, id, owner)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	out := toTransport(row)
	if row.Status == domain.StatusReady {
		u, expires, err := s.objects.PresignDownload(ctx, row.ObjectKey)
		if err != nil {
			return nil, attachmenterrors.Unavailable.Wrap(err)
		}
		out.DownloadUrl = &u
		value := expires.Format(time.RFC3339)
		out.DownloadExpiresAt = &value
	}
	return out, nil
}
func (s *Service) Trash(ctx context.Context, owner int64, id string) error {
	if owner <= 0 || id == "" {
		return attachmenterrors.InvalidInput.New()
	}
	err := s.repo.Trash(ctx, id, owner)
	if errors.Is(err, repository.ErrReferenced) {
		return attachmenterrors.Conflict.Wrap(err)
	}
	return mapRepositoryError(err)
}
func (s *Service) Restore(ctx context.Context, owner int64, id string) error {
	if owner <= 0 || id == "" {
		return attachmenterrors.InvalidInput.New()
	}
	return mapRepositoryError(s.repo.Restore(ctx, id, owner))
}
func (s *Service) ScanOnce(ctx context.Context) error {
	row, err := s.repo.Claim(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	obj, err := s.objects.OpenObject(ctx, row.ObjectKey)
	if err != nil {
		if retryErr := s.repo.RetryScan(ctx, row.ID, err); retryErr != nil {
			return retryErr
		}
		return err
	}
	defer func() { _ = obj.Close() }()
	result, err := s.scanner.Scan(ctx, obj)
	if err != nil {
		if retryErr := s.repo.RetryScan(ctx, row.ID, err); retryErr != nil {
			return retryErr
		}
		return err
	}
	return s.repo.CompleteScan(ctx, row.ID, domain.ScanResult{Clean: result.Clean, SHA256: result.SHA256, Size: result.Size, DetectedType: result.DetectedType}, result.Clean)
}
func toTransport(a *domain.Attachment) *attachmentv1.Attachment {
	if a == nil {
		return nil
	}
	return &attachmentv1.Attachment{Id: a.ID, OwnerId: a.OwnerID, Filename: a.Filename, MediaType: a.MediaType, Category: a.Category, SizeBytes: a.SizeBytes, Sha256: a.SHA256, Status: a.Status, PartSize: int32(a.PartSize), PartCount: a.PartCount, CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339), DetectedType: optional(a.DetectedType)}
}
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func mapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, repository.ErrNotFound) {
		return attachmenterrors.NotFound.New()
	}
	if errors.Is(err, repository.ErrConflict) {
		return attachmenterrors.Conflict.New()
	}
	return attachmenterrors.Internal.Wrap(err)
}

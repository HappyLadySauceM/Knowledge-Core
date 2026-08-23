package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound            = errors.New("attachment not found")
	ErrConflict            = errors.New("attachment state conflict")
	ErrIdempotencyConflict = errors.New("attachment idempotency request conflict")
	ErrReferenced          = errors.New("attachment is referenced")
)

type Idempotency struct {
	OwnerID     int64
	Key         string
	RequestHash string
}

const (
	scanLease       = 30 * time.Minute
	maxScanAttempts = 8
)

type Store struct {
	db  *gorm.DB
	now func() time.Time
}

func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("attachment repository requires database")
	}
	return &Store{db: db, now: time.Now}, nil
}
func toDomain(r *model.Attachment) *domain.Attachment {
	return &domain.Attachment{ID: r.ID, OwnerID: r.OwnerID, Filename: r.Filename, MediaType: r.MediaType, Category: r.Category, SizeBytes: r.SizeBytes, SHA256: r.SHA256, DetectedType: r.DetectedType, ObjectKey: r.ObjectKey, UploadID: r.UploadID, Status: r.Status, PartSize: r.PartSize, PartCount: r.PartCount, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func (s *Store) Create(ctx context.Context, a *domain.Attachment, idempotency Idempotency) (*domain.Attachment, error) {
	if a == nil {
		return nil, errors.New("attachment is required")
	}
	if idempotency.Key != "" {
		if err := domain.ValidateIdempotencyKey(idempotency.Key); err != nil {
			return nil, err
		}
		if len(idempotency.RequestHash) != 64 {
			return nil, errors.New("idempotency request hash is invalid")
		}
	}
	var out *domain.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if idempotency.Key != "" {
			lockKey := fmt.Sprintf("attachment:%d:%s", idempotency.OwnerID, idempotency.Key)
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
				return fmt.Errorf("lock attachment idempotency key: %w", err)
			}
			var existing model.Attachment
			err := tx.Where("owner_id = ? AND idempotency_key = ?", idempotency.OwnerID, idempotency.Key).First(&existing).Error
			if err == nil {
				if existing.RequestHash != idempotency.RequestHash {
					return ErrIdempotencyConflict
				}
				out = toDomain(&existing)
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("read attachment idempotency key: %w", err)
			}
		}
		var used int64
		if err := tx.Model(&model.Attachment{}).Where("owner_id = ? AND status IN ?", a.OwnerID, []string{domain.StatusPendingUpload, domain.StatusScanning, domain.StatusScanParked, domain.StatusReady, domain.StatusTrashed}).Select("COALESCE(SUM(size_bytes),0)").Scan(&used).Error; err != nil {
			return err
		}
		if used+a.SizeBytes > domain.MaxUserBytes {
			return ErrConflict
		}
		rec := &model.Attachment{ID: a.ID, OwnerID: a.OwnerID, Filename: a.Filename, MediaType: a.MediaType, Category: a.Category, SizeBytes: a.SizeBytes, SHA256: a.SHA256, ObjectKey: a.ObjectKey, UploadID: a.UploadID, Status: a.Status, PartSize: a.PartSize, PartCount: a.PartCount, IdempotencyKey: idempotency.Key, RequestHash: idempotency.RequestHash, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
		if err := tx.Create(rec).Error; err != nil {
			return fmt.Errorf("create attachment: %w", err)
		}
		out = toDomain(rec)
		return nil
	})
	return out, err
}
func (s *Store) Get(ctx context.Context, id string, owner int64) (*domain.Attachment, error) {
	var r model.Attachment
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&r).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return toDomain(&r), nil
}
func (s *Store) List(ctx context.Context, owner int64, status, category string, limit int) ([]*domain.Attachment, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := s.db.WithContext(ctx).Where("owner_id = ?", owner)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if category != "" {
		q = q.Where("category = ?", category)
	}
	var rows []model.Attachment
	if err := q.Order("created_at DESC,id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*domain.Attachment, 0, len(rows))
	for i := range rows {
		out = append(out, toDomain(&rows[i]))
	}
	return out, nil
}
func (s *Store) MarkScanning(ctx context.Context, id string, owner int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", id, owner).First(&r).Error; err != nil {
			return ErrNotFound
		}
		if r.Status != domain.StatusPendingUpload {
			return ErrConflict
		}
		now := s.now().UTC()
		if err := tx.Model(&r).Updates(map[string]any{"status": domain.StatusScanning, "updated_at": now}).Error; err != nil {
			return err
		}
		job := &model.ScanJob{AttachmentID: id, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "attachment_id"}}, DoUpdates: clause.Assignments(map[string]any{"attempts": 0, "next_attempt_at": now, "lease_until": nil, "parked_at": nil, "updated_at": now, "last_error": ""})}).Create(job).Error
	})
}
func (s *Store) CompleteScan(ctx context.Context, id string, result domain.ScanResult, clean bool) error {
	status := domain.StatusRejected
	if clean {
		status = domain.StatusReady
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&r).Error; err != nil {
			return err
		}
		// The scanner is authoritative for the content digest. Older rows may
		// have an empty digest because the upload API does not require clients to
		// calculate one; only compare when a digest was already declared.
		if result.Size != r.SizeBytes || (r.SHA256 != "" && !strings.EqualFold(result.SHA256, r.SHA256)) {
			status = domain.StatusRejected
		}
		now := s.now().UTC()
		updates := map[string]any{"status": status, "detected_type": result.DetectedType, "updated_at": now}
		if result.SHA256 != "" {
			updates["sha256"] = strings.ToLower(result.SHA256)
		}
		if err := tx.Model(&r).Updates(updates).Error; err != nil {
			return err
		}
		return tx.Where("attachment_id = ?", id).Delete(&model.ScanJob{}).Error
	})
}
func (s *Store) Claim(ctx context.Context) (*domain.Attachment, error) {
	var result *domain.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		var job model.ScanJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("next_attempt_at <= ? AND (lease_until IS NULL OR lease_until <= ?) AND parked_at IS NULL", now, now).Order("next_attempt_at ASC").First(&job).Error; err != nil {
			return err
		}
		var r model.Attachment
		if err := tx.Where("id = ? AND status = ?", job.AttachmentID, domain.StatusScanning).First(&r).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				_ = tx.Delete(&job).Error
			}
			return err
		}
		leaseUntil := now.Add(scanLease)
		if err := tx.Model(&job).Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "lease_until": leaseUntil, "updated_at": now}).Error; err != nil {
			return err
		}
		result = toDomain(&r)
		return nil
	})
	return result, err
}
func (s *Store) RetryScan(ctx context.Context, id string, scanErr error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", id, domain.StatusScanning).First(&r).Error; err != nil {
			return err
		}
		var job model.ScanJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("attachment_id = ?", id).First(&job).Error; err != nil {
			return err
		}
		now := s.now().UTC()
		lastError := truncateError(scanErr)
		if job.Attempts >= maxScanAttempts {
			if err := tx.Model(&r).Updates(map[string]any{"status": domain.StatusScanParked, "updated_at": now}).Error; err != nil {
				return err
			}
			return tx.Model(&job).Updates(map[string]any{"parked_at": now, "lease_until": nil, "last_error": lastError, "updated_at": now}).Error
		}
		backoff := time.Second * time.Duration(1<<min(job.Attempts, 7))
		return tx.Model(&job).Updates(map[string]any{"next_attempt_at": now.Add(backoff), "lease_until": nil, "last_error": lastError, "updated_at": now}).Error
	})
}
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 255 {
		return value[:255]
	}
	return value
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (s *Store) Trash(ctx context.Context, id string, owner int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var r model.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", id, owner).First(&r).Error; err != nil {
			return ErrNotFound
		}
		var refs int64
		if err := tx.Model(&model.Reference{}).Where("attachment_id = ?", id).Count(&refs).Error; err != nil {
			return err
		}
		if refs > 0 {
			return ErrReferenced
		}
		if r.Status != domain.StatusReady && r.Status != domain.StatusRejected && r.Status != domain.StatusScanParked {
			return ErrConflict
		}
		return tx.Model(&r).Updates(map[string]any{"status": domain.StatusTrashed, "updated_at": s.now().UTC()}).Error
	})
}
func (s *Store) Restore(ctx context.Context, id string, owner int64) error {
	return s.db.WithContext(ctx).Model(&model.Attachment{}).Where("id = ? AND owner_id = ? AND status = ?", id, owner, domain.StatusTrashed).Updates(map[string]any{"status": domain.StatusReady, "updated_at": s.now().UTC()}).Error
}

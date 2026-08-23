package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound   = errors.New("attachment not found")
	ErrConflict   = errors.New("attachment state conflict")
	ErrReferenced = errors.New("attachment is referenced")
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
func (s *Store) Create(ctx context.Context, a *domain.Attachment) (*domain.Attachment, error) {
	if a == nil {
		return nil, errors.New("attachment is required")
	}
	var out *domain.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var used int64
		if err := tx.Model(&model.Attachment{}).Where("owner_id = ? AND status IN ?", a.OwnerID, []string{domain.StatusPendingUpload, domain.StatusScanning, domain.StatusReady, domain.StatusTrashed}).Select("COALESCE(SUM(size_bytes),0)").Scan(&used).Error; err != nil {
			return err
		}
		if used+a.SizeBytes > domain.MaxUserBytes {
			return ErrConflict
		}
		rec := &model.Attachment{ID: a.ID, OwnerID: a.OwnerID, Filename: a.Filename, MediaType: a.MediaType, Category: a.Category, SizeBytes: a.SizeBytes, SHA256: a.SHA256, ObjectKey: a.ObjectKey, UploadID: a.UploadID, Status: a.Status, PartSize: a.PartSize, PartCount: a.PartCount, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
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
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "attachment_id"}}, DoUpdates: clause.Assignments(map[string]any{"next_attempt_at": now, "updated_at": now, "last_error": ""})}).Create(job).Error
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
		if result.Size != r.SizeBytes || result.SHA256 != r.SHA256 {
			status = domain.StatusRejected
		}
		now := s.now().UTC()
		return tx.Model(&r).Updates(map[string]any{"status": status, "detected_type": result.DetectedType, "updated_at": now}).Error
	})
}
func (s *Store) Claim(ctx context.Context) (*domain.Attachment, error) {
	var result *domain.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job model.ScanJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("next_attempt_at <= ?", s.now().UTC()).Order("next_attempt_at ASC").First(&job).Error; err != nil {
			return err
		}
		var r model.Attachment
		if err := tx.Where("id = ? AND status = ?", job.AttachmentID, domain.StatusScanning).First(&r).Error; err != nil {
			return err
		}
		if err := tx.Delete(&job).Error; err != nil {
			return err
		}
		result = toDomain(&r)
		return nil
	})
	return result, err
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
		if r.Status != domain.StatusReady && r.Status != domain.StatusRejected {
			return ErrConflict
		}
		return tx.Model(&r).Updates(map[string]any{"status": domain.StatusTrashed, "updated_at": s.now().UTC()}).Error
	})
}
func (s *Store) Restore(ctx context.Context, id string, owner int64) error {
	return s.db.WithContext(ctx).Model(&model.Attachment{}).Where("id = ? AND owner_id = ? AND status = ?", id, owner, domain.StatusTrashed).Updates(map[string]any{"status": domain.StatusReady, "updated_at": s.now().UTC()}).Error
}

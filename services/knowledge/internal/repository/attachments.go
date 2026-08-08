package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListAttachments(ctx context.Context, documentID string, actorID int64) ([]*domain.Attachment, error) {
	document, err := s.GetDocument(ctx, documentID, actorID, false)
	if err != nil {
		return nil, err
	}
	if !domain.CanRead(document.Access) {
		return nil, ErrForbidden
	}
	var records []model.Attachment
	if err := s.db.WithContext(ctx).Where("document_id = ?", documentID).
		Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list document attachments: %w", err)
	}
	result := make([]*domain.Attachment, 0, len(records))
	for index := range records {
		result = append(result, attachmentFromModel(&records[index]))
	}
	return result, nil
}

func (s *Store) CreateAttachment(ctx context.Context, attachment *domain.Attachment, actorID int64, idempotency Idempotency) (bool, error) {
	if attachment == nil {
		return false, fmt.Errorf("create attachment: attachment is required")
	}
	var stored *domain.Attachment
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, access, err := lockDocument(tx, attachment.DocumentID, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		if err := lockAttachmentQuota(tx, actorID); err != nil {
			return err
		}
		existingID, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			var existing model.Attachment
			if err := tx.Where("id = ? AND document_id = ?", existingID, attachment.DocumentID).First(&existing).Error; err != nil {
				return mapNotFound("load idempotent attachment", err)
			}
			stored = attachmentFromModel(&existing)
			return nil
		}
		activeStatuses := []string{domain.AttachmentPendingUpload, domain.AttachmentScanning, domain.AttachmentReady}
		var documentBytes int64
		if err := tx.Model(&model.Attachment{}).Where("document_id = ? AND status IN ?", attachment.DocumentID, activeStatuses).
			Select("COALESCE(SUM(size_bytes), 0)").Scan(&documentBytes).Error; err != nil {
			return fmt.Errorf("calculate document attachment quota: %w", err)
		}
		if documentBytes+attachment.SizeBytes > domain.MaxDocumentBytes {
			return ErrQuotaExceeded
		}
		var userBytes int64
		if err := tx.Model(&model.Attachment{}).Where("uploader_id = ? AND status IN ?", actorID, activeStatuses).
			Select("COALESCE(SUM(size_bytes), 0)").Scan(&userBytes).Error; err != nil {
			return fmt.Errorf("calculate user attachment quota: %w", err)
		}
		if userBytes+attachment.SizeBytes > domain.MaxUserAttachmentBytes {
			return ErrQuotaExceeded
		}
		record := attachmentToModel(attachment)
		if err := tx.Create(record).Error; err != nil {
			return mapWriteError("create attachment", err)
		}
		if err := s.saveIdempotency(tx, idempotency, attachment.ID); err != nil {
			return err
		}
		stored = attachmentFromModel(record)
		created = true
		return nil
	})
	if err != nil {
		return false, err
	}
	*attachment = *stored
	return created, nil
}

func (s *Store) ListReadyAttachments(ctx context.Context, documentID string) ([]*domain.Attachment, error) {
	var records []model.Attachment
	if err := s.db.WithContext(ctx).Where("document_id = ? AND status = ?", documentID, domain.AttachmentReady).
		Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list published document attachments: %w", err)
	}
	result := make([]*domain.Attachment, 0, len(records))
	for index := range records {
		result = append(result, attachmentFromModel(&records[index]))
	}
	return result, nil
}

func (s *Store) GetAttachment(ctx context.Context, documentID, attachmentID string, actorID int64, requireEdit bool) (*domain.Attachment, error) {
	document, err := s.GetDocument(ctx, documentID, actorID, false)
	if err != nil {
		return nil, err
	}
	if requireEdit && !domain.CanEdit(document.Access) || !requireEdit && !domain.CanRead(document.Access) {
		return nil, ErrForbidden
	}
	var record model.Attachment
	if err := s.db.WithContext(ctx).Where("id = ? AND document_id = ?", attachmentID, documentID).First(&record).Error; err != nil {
		return nil, mapNotFound("get document attachment", err)
	}
	return attachmentFromModel(&record), nil
}

func (s *Store) GetAttachmentContent(ctx context.Context, attachmentID string, actorID int64) (*domain.Attachment, error) {
	var record model.Attachment
	if err := s.db.WithContext(ctx).Where("id = ? AND status = ?", attachmentID, domain.AttachmentReady).First(&record).Error; err != nil {
		return nil, mapNotFound("get attachment content", err)
	}
	var document model.Document
	if err := s.db.WithContext(ctx).Where("id = ?", record.DocumentID).First(&document).Error; err != nil {
		return nil, mapNotFound("get attachment document", err)
	}
	if document.DeletedAt != nil {
		return nil, ErrGone
	}
	access, err := accessFor(s.db.WithContext(ctx), &document, actorID)
	if err != nil {
		return nil, err
	}
	if !document.Published && !domain.CanRead(access) {
		return nil, ErrForbidden
	}
	return attachmentFromModel(&record), nil
}

func (s *Store) MarkAttachmentScanning(ctx context.Context, documentID, attachmentID string, actorID int64) (*domain.Attachment, error) {
	var result *domain.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, access, err := lockDocument(tx, documentID, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		var record model.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND document_id = ?", attachmentID, documentID).First(&record).Error; err != nil {
			return mapNotFound("lock attachment completion", err)
		}
		if record.Status != domain.AttachmentPendingUpload || !record.UploadExpires.After(s.now().UTC()) {
			return ErrConflict
		}
		now := s.now().UTC()
		if err := tx.Model(&record).Updates(map[string]any{"status": domain.AttachmentScanning, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("mark attachment scanning: %w", err)
		}
		job := model.AttachmentScanJob{AttachmentID: attachmentID, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&job).Error; err != nil {
			return mapWriteError("enqueue attachment scan", err)
		}
		record.Status = domain.AttachmentScanning
		record.UpdatedAt = now
		result = attachmentFromModel(&record)
		return nil
	})
	return result, err
}

func (s *Store) QueueAttachmentDeletion(ctx context.Context, documentID, attachmentID string, actorID int64) (*domain.Attachment, error) {
	var result *domain.Attachment
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, access, err := lockDocument(tx, documentID, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		var record model.Attachment
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND document_id = ?", attachmentID, documentID).First(&record).Error; err != nil {
			return mapNotFound("lock attachment deletion", err)
		}
		if record.Status == domain.AttachmentDeleting {
			result = attachmentFromModel(&record)
			return nil
		}
		now := s.now().UTC()
		if err := tx.Model(&record).Updates(map[string]any{"status": domain.AttachmentDeleting, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("queue attachment deletion: %w", err)
		}
		job := model.AttachmentScanJob{AttachmentID: attachmentID, NextAttemptAt: now, LastErrorKey: "delete", CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "attachment_id"}},
			DoUpdates: clause.Assignments(map[string]any{"next_attempt_at": now, "last_error_key": "delete", "updated_at": now}),
		}).Create(&job).Error; err != nil {
			return fmt.Errorf("enqueue attachment deletion: %w", err)
		}
		record.Status = domain.AttachmentDeleting
		record.UpdatedAt = now
		result = attachmentFromModel(&record)
		return nil
	})
	return result, err
}

func (s *Store) ClaimAttachmentJobs(ctx context.Context, limit int, lease time.Duration) ([]domain.ScanJob, error) {
	if limit <= 0 {
		limit = 10
	}
	var jobs []domain.ScanJob
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []model.AttachmentScanJob
		now := s.now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("next_attempt_at <= ?", now).
			Order("next_attempt_at ASC, created_at ASC, attachment_id ASC").Limit(limit).Find(&records).Error; err != nil {
			return fmt.Errorf("claim attachment jobs: %w", err)
		}
		for index := range records {
			var attachment model.Attachment
			if err := tx.Where("id = ?", records[index].AttachmentID).First(&attachment).Error; err != nil {
				return mapNotFound("load attachment job resource", err)
			}
			attempts := records[index].Attempts + 1
			if err := tx.Model(&model.AttachmentScanJob{}).Where("attachment_id = ?", attachment.ID).
				Updates(map[string]any{"attempts": attempts, "next_attempt_at": now.Add(lease), "updated_at": now}).Error; err != nil {
				return fmt.Errorf("lease attachment job: %w", err)
			}
			jobs = append(jobs, domain.ScanJob{Attachment: *attachmentFromModel(&attachment), Attempts: attempts})
		}
		return nil
	})
	return jobs, err
}

func (s *Store) MarkAttachmentReady(ctx context.Context, attachmentID, detectedType string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		result := tx.Model(&model.Attachment{}).Where("id = ? AND status = ?", attachmentID, domain.AttachmentScanning).
			Updates(map[string]any{"status": domain.AttachmentReady, "detected_type": detectedType, "failure_reason": "", "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("mark attachment ready: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrPrecondition
		}
		if err := tx.Where("attachment_id = ?", attachmentID).Delete(&model.AttachmentScanJob{}).Error; err != nil {
			return fmt.Errorf("complete attachment scan job: %w", err)
		}
		return nil
	})
}

func (s *Store) MarkAttachmentRejected(ctx context.Context, attachmentID, reason string) error {
	now := s.now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.Attachment{}).Where("id = ? AND status = ?", attachmentID, domain.AttachmentScanning).
			Updates(map[string]any{"status": domain.AttachmentRejected, "failure_reason": reason, "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("mark attachment rejected: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrPrecondition
		}
		if err := tx.Model(&model.AttachmentScanJob{}).Where("attachment_id = ?", attachmentID).
			Updates(map[string]any{"next_attempt_at": now, "last_error_key": "cleanup", "updated_at": now}).Error; err != nil {
			return fmt.Errorf("queue rejected attachment cleanup: %w", err)
		}
		return nil
	})
}

func (s *Store) FinishAttachmentCleanup(ctx context.Context, attachmentID string, removeRecord bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if removeRecord {
			if err := tx.Where("id = ?", attachmentID).Delete(&model.Attachment{}).Error; err != nil {
				return fmt.Errorf("delete attachment record: %w", err)
			}
			return nil
		}
		if err := tx.Where("attachment_id = ?", attachmentID).Delete(&model.AttachmentScanJob{}).Error; err != nil {
			return fmt.Errorf("finish attachment cleanup job: %w", err)
		}
		return nil
	})
}

func (s *Store) RetryAttachmentJob(ctx context.Context, attachmentID, errorKey string, attempts int) error {
	delay := attachmentRetryDelay(attempts)
	return s.db.WithContext(ctx).Model(&model.AttachmentScanJob{}).Where("attachment_id = ?", attachmentID).
		Updates(map[string]any{"next_attempt_at": s.now().UTC().Add(delay), "last_error_key": errorKey, "updated_at": s.now().UTC()}).Error
}

func attachmentRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

func (s *Store) QueueExpiredUploads(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []model.Attachment
		now := s.now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ? AND upload_expires <= ?", domain.AttachmentPendingUpload, now).
			Order("upload_expires ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
			return fmt.Errorf("find expired attachment uploads: %w", err)
		}
		for index := range records {
			if err := tx.Model(&records[index]).Updates(map[string]any{"status": domain.AttachmentDeleting, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("expire attachment upload: %w", err)
			}
			job := model.AttachmentScanJob{
				AttachmentID: records[index].ID, NextAttemptAt: now, LastErrorKey: "expired_upload",
				CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
				return fmt.Errorf("queue expired upload cleanup: %w", err)
			}
		}
		return nil
	})
}

func lockAttachmentQuota(tx *gorm.DB, uploaderID int64) error {
	lockKey := fmt.Sprintf("knowledge:attachment-quota:%d", uploaderID)
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
		return fmt.Errorf("lock attachment uploader quota: %w", err)
	}
	return nil
}

func attachmentToModel(value *domain.Attachment) *model.Attachment {
	return &model.Attachment{
		ID: value.ID, DocumentID: value.DocumentID, UploaderID: value.UploaderID,
		Filename: value.Filename, DeclaredType: value.DeclaredType, DetectedType: value.DetectedType,
		SizeBytes: value.SizeBytes, SHA256: value.SHA256, ObjectKey: value.ObjectKey,
		Status: value.Status, FailureReason: value.FailureReason, UploadExpires: value.UploadExpires.UTC(),
		CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(),
	}
}

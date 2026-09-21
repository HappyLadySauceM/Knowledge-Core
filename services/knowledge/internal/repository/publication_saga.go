package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	publicationActionPublish = "publish"
	publicationActionClear   = "clear"
	publicationJobPending    = "pending"
	publicationJobStaged     = "staged"
	publicationJobPromoted   = "promoted"
)

func (s *Store) PublishSnapshot(ctx context.Context, id string, actorID, expected int64, input PublicationSnapshotInput) (*domain.Document, error) {
	content, err := jsoncodec.Marshal(input.Content)
	if err != nil {
		return nil, fmt.Errorf("encode publication candidate content: %w", err)
	}
	tags, err := jsoncodec.Marshal(input.Tags)
	if err != nil {
		return nil, fmt.Errorf("encode publication candidate tags: %w", err)
	}
	mediaIDs, err := jsoncodec.Marshal(input.MediaIDs)
	if err != nil {
		return nil, fmt.Errorf("encode publication candidate media: %w", err)
	}
	var result *domain.Document
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, input.Idempotency)
		if err != nil {
			return err
		}
		if found {
			result, err = s.getDocument(tx, existingID, actorID, false)
			return err
		}
		record, access, err := lockDocument(tx, id, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		if record.MetadataRevision != expected {
			return ErrPrecondition
		}
		var other model.DocumentPublication
		if err := tx.Where("lower(slug) = lower(?) AND document_id <> ?", input.Slug, id).First(&other).Error; err == nil {
			return ErrConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check live publication slug: %w", err)
		}
		var otherCandidate model.PublicationCandidate
		if err := tx.Where("lower(slug) = lower(?) AND document_id <> ?", input.Slug, id).First(&otherCandidate).Error; err == nil {
			return ErrConflict
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("check candidate publication slug: %w", err)
		}
		now := s.now().UTC()
		generation := record.PublicationGeneration + 1
		candidate := &model.PublicationCandidate{
			DocumentID: id, Generation: generation,
			Title: input.Title, Summary: input.Summary, Slug: input.Slug, Language: input.Language, Tags: tags,
			OwnerID: record.OwnerID, OwnerUsername: record.OwnerUsername, OwnerAvatar: record.OwnerAvatar,
			Content: content, PlainText: input.PlainText, MediaIDs: mediaIDs, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Save(candidate).Error; err != nil {
			return mapWriteError("save publication candidate", err)
		}
		if err := tx.Where("document_id = ? AND parked_at IS NULL", id).Delete(&model.PublicationReferenceJob{}).Error; err != nil {
			return fmt.Errorf("supersede publication reference job: %w", err)
		}
		if err := s.enqueuePublicationReferenceJob(tx, record, generation, publicationActionPublish, input.MediaIDs, now); err != nil {
			return err
		}
		if err := tx.Model(record).Updates(map[string]any{
			"publication_status": domain.PublicationPublishing, "publication_error": nil,
			"publication_generation": generation, "metadata_revision": gorm.Expr("metadata_revision + 1"), "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("mark publication pending: %w", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload pending publication: %w", err)
		}
		if err := s.saveIdempotency(tx, input.Idempotency, id); err != nil {
			return err
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		return s.loadDocumentStructure(tx, result)
	})
	return result, err
}

func (s *Store) SetPublication(ctx context.Context, id string, actorID, expected int64, published bool, idempotency Idempotency) (*domain.Document, error) {
	if published {
		return nil, ErrPrecondition
	}
	var result *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			result, err = s.getDocument(tx, existingID, actorID, false)
			return err
		}
		record, access, err := lockDocument(tx, id, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		if record.MetadataRevision != expected {
			return ErrPrecondition
		}
		now := s.now().UTC()
		generation := record.PublicationGeneration + 1
		if err := tx.Where("document_id = ?", id).Delete(&model.PublicationCandidate{}).Error; err != nil {
			return fmt.Errorf("remove publication candidate: %w", err)
		}
		if err := tx.Where("document_id = ?", id).Delete(&model.DocumentPublication{}).Error; err != nil {
			return fmt.Errorf("remove live publication: %w", err)
		}
		if err := tx.Where("document_id = ? AND parked_at IS NULL", id).Delete(&model.PublicationReferenceJob{}).Error; err != nil {
			return fmt.Errorf("supersede publication cleanup job: %w", err)
		}
		if err := s.enqueuePublicationReferenceJob(tx, record, generation, publicationActionClear, nil, now); err != nil {
			return err
		}
		if err := tx.Model(record).Updates(map[string]any{
			"published_at": nil, "publication_status": domain.PublicationUnpublishing,
			"publication_error": nil, "publication_generation": generation,
			"metadata_revision": gorm.Expr("metadata_revision + 1"), "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("mark document unpublishing: %w", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload unpublishing document: %w", err)
		}
		if err := s.saveIdempotency(tx, idempotency, id); err != nil {
			return err
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		return s.loadDocumentStructure(tx, result)
	})
	return result, err
}

func (s *Store) enqueuePublicationReferenceJob(tx *gorm.DB, document *model.Document, generation int64, action string, attachmentIDs []string, now time.Time) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	ids, err := jsoncodec.Marshal(attachmentIDs)
	if err != nil {
		return fmt.Errorf("encode publication reference job: %w", err)
	}
	headers, err := jsoncodec.Marshal(coretrace.PropagationHeaders(tx.Statement.Context))
	if err != nil {
		return fmt.Errorf("encode publication reference trace context: %w", err)
	}
	job := &model.PublicationReferenceJob{
		ID: id, DocumentID: document.ID, OwnerID: document.OwnerID, Generation: generation,
		Action: action, AttachmentIDs: ids, State: publicationJobPending, NextAttemptAt: now,
		TraceHeaders: headers, CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Create(job).Error; err != nil {
		return mapWriteError("enqueue publication reference job", err)
	}
	return notifyWorkers(tx, WorkerWakePayloadPublication)
}

func (s *Store) ClaimPublicationReferenceJobs(ctx context.Context, limit int, lease time.Duration) ([]domain.PublicationReferenceJob, error) {
	if limit <= 0 {
		limit = 20
	}
	var result []domain.PublicationReferenceJob
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		var jobs []model.PublicationReferenceJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("parked_at IS NULL AND next_attempt_at <= ? AND (lease_until IS NULL OR lease_until <= ?)", now, now).
			Order("next_attempt_at ASC, created_at ASC, id ASC").Limit(limit).Find(&jobs).Error; err != nil {
			return fmt.Errorf("claim publication reference jobs: %w", err)
		}
		for index := range jobs {
			leaseUntil := now.Add(lease)
			if err := tx.Model(&jobs[index]).Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "lease_until": leaseUntil, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("lease publication reference job: %w", err)
			}
			var ids []string
			if err := jsoncodec.Unmarshal(jobs[index].AttachmentIDs, &ids); err != nil {
				return fmt.Errorf("decode publication reference job attachments: %w", err)
			}
			result = append(result, domain.PublicationReferenceJob{
				ID: jobs[index].ID, DocumentID: jobs[index].DocumentID, OwnerID: jobs[index].OwnerID,
				Generation: jobs[index].Generation, Action: jobs[index].Action, AttachmentIDs: ids,
				State: jobs[index].State, Attempts: jobs[index].Attempts + 1, Headers: decodeTraceHeaders(jobs[index].TraceHeaders),
			})
		}
		return nil
	})
	return result, err
}

func (s *Store) MarkPublicationReferencesStaged(ctx context.Context, id string) error {
	return s.updatePublicationJob(ctx, id, publicationJobStaged, "")
}

func (s *Store) MarkPublicationPromoted(ctx context.Context, id string) error {
	return s.updatePublicationJob(ctx, id, publicationJobPromoted, "")
}

func (s *Store) updatePublicationJob(ctx context.Context, id, state, errorKey string) error {
	result := s.db.WithContext(ctx).Model(&model.PublicationReferenceJob{}).Where("id = ? AND parked_at IS NULL", id).
		Updates(map[string]any{"state": state, "last_error_key": errorKey, "lease_until": nil, "next_attempt_at": s.now().UTC(), "updated_at": s.now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("update publication reference job: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) PromotePublication(ctx context.Context, job domain.PublicationReferenceJob) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stored model.PublicationReferenceJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", job.ID).First(&stored).Error; err != nil {
			return mapNotFound("lock publication reference job", err)
		}
		if stored.State == publicationJobPromoted {
			return nil
		}
		if stored.State != publicationJobStaged || stored.Generation != job.Generation {
			return ErrPrecondition
		}
		record, _, err := lockDocument(tx, job.DocumentID, job.OwnerID, false)
		if err != nil {
			return err
		}
		if record.PublicationGeneration != job.Generation || record.PublicationStatus != domain.PublicationPublishing {
			return ErrPrecondition
		}
		var candidate model.PublicationCandidate
		if err := tx.Where("document_id = ? AND generation = ?", job.DocumentID, job.Generation).First(&candidate).Error; err != nil {
			return mapNotFound("load publication candidate", err)
		}
		now := s.now().UTC()
		var candidateTags []string
		if err := jsoncodec.Unmarshal(candidate.Tags, &candidateTags); err != nil {
			return fmt.Errorf("decode publication candidate tags: %w", err)
		}
		publication := &model.DocumentPublication{
			DocumentID: candidate.DocumentID,
			Title:      candidate.Title, Summary: candidate.Summary, Slug: candidate.Slug, Language: candidate.Language,
			Tags: candidate.Tags, OwnerID: candidate.OwnerID, OwnerUsername: candidate.OwnerUsername, OwnerAvatar: candidate.OwnerAvatar,
			Content: candidate.Content, PlainText: candidate.PlainText, PublishedAt: now, UpdatedAt: now,
		}
		if err := tx.Save(publication).Error; err != nil {
			return mapWriteError("promote publication candidate", err)
		}
		if err := s.replaceDocumentTags(tx, record.OwnerID, job.DocumentID, candidateTags, now); err != nil {
			return err
		}
		if err := tx.Where("document_id = ?", job.DocumentID).Delete(&model.PublishedMediaReference{}).Error; err != nil {
			return fmt.Errorf("replace published media references: %w", err)
		}
		for _, attachmentID := range job.AttachmentIDs {
			ref := &model.PublishedMediaReference{DocumentID: job.DocumentID, AttachmentID: attachmentID, Generation: job.Generation, CreatedAt: now}
			if err := tx.Create(ref).Error; err != nil {
				return mapWriteError("create published media reference", err)
			}
		}
		var alias model.SlugAlias
		aliasErr := tx.Where("slug = ?", candidate.Slug).First(&alias).Error
		if errors.Is(aliasErr, gorm.ErrRecordNotFound) {
			if err := tx.Create(&model.SlugAlias{Slug: candidate.Slug, DocumentID: stringPointer(job.DocumentID), CreatedAt: now}).Error; err != nil {
				return mapWriteError("reserve publication slug", err)
			}
		} else if aliasErr != nil {
			return fmt.Errorf("load publication slug: %w", aliasErr)
		} else if alias.DocumentID != nil && *alias.DocumentID != job.DocumentID {
			return ErrConflict
		} else if err := tx.Model(&alias).Updates(map[string]any{"document_id": job.DocumentID, "gone_at": nil}).Error; err != nil {
			return fmt.Errorf("activate publication slug: %w", err)
		}
		if err := tx.Model(record).Updates(map[string]any{
			"published_at": now, "publication_status": domain.PublicationPublished,
			"publication_error": nil, "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("activate publication: %w", err)
		}
		if err := tx.Where("document_id = ?", job.DocumentID).Delete(&model.PublicationCandidate{}).Error; err != nil {
			return fmt.Errorf("remove promoted publication candidate: %w", err)
		}
		if err := tx.Model(&stored).Updates(map[string]any{"state": publicationJobPromoted, "lease_until": nil, "next_attempt_at": now, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("mark publication promoted: %w", err)
		}
		return nil
	})
}

func (s *Store) CompletePublicationReferenceJob(ctx context.Context, id string, clear bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job model.PublicationReferenceJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&job).Error; err != nil {
			return mapNotFound("complete publication reference job", err)
		}
		if clear {
			// Attachment cleanup is the cross-service part of this job. Remove
			// only the same-generation Knowledge references here so a stale
			// unpublish completion can never erase a newer publication's refs.
			if err := tx.Where("document_id = ? AND generation = ?", job.DocumentID, job.Generation).
				Delete(&model.PublishedMediaReference{}).Error; err != nil {
				return fmt.Errorf("remove cleared publication media references: %w", err)
			}
			if err := tx.Model(&model.Document{}).Where("id = ? AND publication_generation = ?", job.DocumentID, job.Generation).
				Updates(map[string]any{"publication_status": domain.PublicationDraft, "publication_error": nil, "updated_at": s.now().UTC()}).Error; err != nil {
				return fmt.Errorf("complete publication cleanup: %w", err)
			}
		}
		return tx.Delete(&job).Error
	})
}

// HasPendingPublicationReferenceClear reports whether a document still has a
// durable attachment-cleanup command. Purge must wait for this row to be
// completed; deleting the document first would cascade the command and make a
// transient Attachment outage unrecoverable.
func (s *Store) HasPendingPublicationReferenceClear(ctx context.Context, documentID string) (bool, error) {
	var count int64
	if err := s.db.WithContext(ctx).Model(&model.PublicationReferenceJob{}).
		Where("document_id = ? AND action = ?", documentID, publicationActionClear).
		Count(&count).Error; err != nil {
		return false, fmt.Errorf("check publication reference cleanup: %w", err)
	}
	return count > 0, nil
}

// RedrivePublicationReferenceClear makes a parked clear command claimable
// again when its document is already awaiting permanent deletion. The normal
// unpublish path intentionally leaves parked jobs for explicit operator
// redrive; purge is the exceptional path that must not strand cleanup forever.
func (s *Store) RedrivePublicationReferenceClear(ctx context.Context, documentID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		result := tx.Model(&model.PublicationReferenceJob{}).
			Where("document_id = ? AND action = ? AND parked_at IS NOT NULL", documentID, publicationActionClear).
			Updates(map[string]any{
				"parked_at": nil, "attempts": 0, "lease_until": nil,
				"next_attempt_at": now, "last_error_key": "purge_reference_redrive", "updated_at": now,
			})
		if result.Error != nil {
			return fmt.Errorf("redrive publication reference cleanup: %w", result.Error)
		}
		if result.RowsAffected > 0 {
			return notifyWorkers(tx, WorkerWakePayloadPublication)
		}
		return nil
	})
}

func (s *Store) RetryPublicationReferenceJob(ctx context.Context, id, errorKey string, attempts int) error {
	delay := publicationRetryDelay(attempts)
	result := s.db.WithContext(ctx).Model(&model.PublicationReferenceJob{}).Where("id = ? AND parked_at IS NULL", id).
		Updates(map[string]any{"next_attempt_at": s.now().UTC().Add(delay), "lease_until": nil, "last_error_key": errorKey, "updated_at": s.now().UTC()})
	return result.Error
}

func publicationRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	base := time.Duration(1<<(attempt-1)) * time.Second
	// Stable jitter prevents synchronized retries without making recovery tests flaky.
	jitter := time.Duration((attempt*7919)%1000) * time.Millisecond
	return base + jitter
}

func (s *Store) ParkPublicationReferenceJob(ctx context.Context, id, errorKey string, publish bool) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		var job model.PublicationReferenceJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&job).Error; err != nil {
			return mapNotFound("park publication reference job", err)
		}
		if err := tx.Model(&job).Updates(map[string]any{"parked_at": now, "lease_until": nil, "last_error_key": errorKey, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("park publication reference job: %w", err)
		}
		status := domain.PublicationUnpublishFailed
		if publish {
			status = domain.PublicationPublishFailed
		}
		return tx.Model(&model.Document{}).Where("id = ? AND publication_generation = ?", job.DocumentID, job.Generation).
			Updates(map[string]any{"publication_status": status, "publication_error": errorKey, "updated_at": now}).Error
	})
}

func (s *Store) IsMediaPublished(ctx context.Context, attachmentID string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.PublishedMediaReference{}).
		Joins("JOIN knowledge.documents d ON d.id = knowledge.published_media_references.document_id").
		Where("knowledge.published_media_references.attachment_id = ? AND d.publication_status = 'published' AND d.deleted_at IS NULL", attachmentID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("authorize published media: %w", err)
	}
	return count > 0, nil
}

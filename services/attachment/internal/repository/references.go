package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const publicationReferenceType = "document_publication"

type PublicationReferenceCommand struct {
	DocumentID    string
	OwnerID       int64
	Generation    int64
	AttachmentIDs []string
	RequestHash   string
}

type PublicationReferenceState struct {
	DocumentID       string
	ActiveGeneration int64
	StagedGeneration *int64
}

func lockReferenceHead(tx *gorm.DB, documentID string) (*model.ReferenceHead, error) {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "attachment:publication:"+documentID).Error; err != nil {
		return nil, fmt.Errorf("lock publication references: %w", err)
	}
	var head model.ReferenceHead
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("ref_type = ? AND ref_id = ?", publicationReferenceType, documentID).First(&head).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read publication reference head: %w", err)
	}
	return &head, nil
}

func (s *Store) StagePublicationReferences(ctx context.Context, command PublicationReferenceCommand) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		head, err := lockReferenceHead(tx, command.DocumentID)
		if err != nil {
			return err
		}
		if head != nil {
			latest := head.ActiveGeneration
			if head.StagedGeneration != nil && *head.StagedGeneration > latest {
				latest = *head.StagedGeneration
			}
			if command.Generation < latest {
				return nil
			}
			if head.StagedGeneration != nil && command.Generation == *head.StagedGeneration {
				if head.StagedHash != command.RequestHash || head.OwnerID != command.OwnerID {
					return ErrIdempotencyConflict
				}
				return nil
			}
			if command.Generation == head.ActiveGeneration && head.StagedGeneration == nil {
				if head.ActiveHash != command.RequestHash || head.OwnerID != command.OwnerID {
					return ErrIdempotencyConflict
				}
				return nil
			}
		}

		if len(command.AttachmentIDs) > 0 {
			var count int64
			if err := tx.Model(&model.Attachment{}).
				Where("id IN ? AND owner_id = ? AND status = ?", command.AttachmentIDs, command.OwnerID, domain.StatusReady).
				Count(&count).Error; err != nil {
				return fmt.Errorf("validate publication attachments: %w", err)
			}
			if count != int64(len(command.AttachmentIDs)) {
				return ErrConflict
			}
		}

		activeGeneration := int64(0)
		if head != nil {
			activeGeneration = head.ActiveGeneration
			if head.StagedGeneration != nil && *head.StagedGeneration != activeGeneration {
				if err := tx.Where("ref_type = ? AND ref_id = ? AND generation = ?", publicationReferenceType, command.DocumentID, *head.StagedGeneration).Delete(&model.Reference{}).Error; err != nil {
					return fmt.Errorf("remove superseded staged references: %w", err)
				}
			}
		}
		if err := tx.Where("ref_type = ? AND ref_id = ? AND generation = ?", publicationReferenceType, command.DocumentID, command.Generation).Delete(&model.Reference{}).Error; err != nil {
			return fmt.Errorf("replace staged publication references: %w", err)
		}
		now := s.now().UTC()
		for _, attachmentID := range command.AttachmentIDs {
			reference := model.Reference{AttachmentID: attachmentID, RefType: publicationReferenceType, RefID: command.DocumentID, Generation: command.Generation, CreatedAt: now}
			if err := tx.Create(&reference).Error; err != nil {
				return fmt.Errorf("stage publication reference: %w", err)
			}
		}
		if head == nil {
			head = &model.ReferenceHead{RefType: publicationReferenceType, RefID: command.DocumentID, OwnerID: command.OwnerID, ActiveGeneration: 0, ActiveHash: "", UpdatedAt: now}
			if err := tx.Create(head).Error; err != nil {
				return fmt.Errorf("create publication reference head: %w", err)
			}
		}
		return tx.Model(head).Updates(map[string]any{
			"owner_id": command.OwnerID, "staged_generation": command.Generation,
			"staged_hash": command.RequestHash, "updated_at": now,
		}).Error
	})
}

func (s *Store) FinalizePublicationReferences(ctx context.Context, command PublicationReferenceCommand) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		head, err := lockReferenceHead(tx, command.DocumentID)
		if err != nil {
			return err
		}
		if head == nil {
			return ErrNotFound
		}
		if head.ActiveGeneration > command.Generation {
			return nil
		}
		if head.ActiveGeneration == command.Generation && head.StagedGeneration == nil {
			if head.ActiveHash != command.RequestHash || head.OwnerID != command.OwnerID {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if head.StagedGeneration == nil || *head.StagedGeneration != command.Generation || head.StagedHash != command.RequestHash || head.OwnerID != command.OwnerID {
			return ErrConflict
		}
		if err := tx.Where("ref_type = ? AND ref_id = ? AND generation < ?", publicationReferenceType, command.DocumentID, command.Generation).Delete(&model.Reference{}).Error; err != nil {
			return fmt.Errorf("remove superseded publication references: %w", err)
		}
		return tx.Model(head).Updates(map[string]any{
			"active_generation": command.Generation, "active_hash": command.RequestHash,
			"staged_generation": nil, "staged_hash": "", "updated_at": s.now().UTC(),
		}).Error
	})
}

func (s *Store) ClearPublicationReferences(ctx context.Context, command PublicationReferenceCommand) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		head, err := lockReferenceHead(tx, command.DocumentID)
		if err != nil {
			return err
		}
		if head != nil {
			latest := head.ActiveGeneration
			if head.StagedGeneration != nil && *head.StagedGeneration > latest {
				latest = *head.StagedGeneration
			}
			if command.Generation < latest {
				return nil
			}
		}
		if err := tx.Where("ref_type = ? AND ref_id = ?", publicationReferenceType, command.DocumentID).Delete(&model.Reference{}).Error; err != nil {
			return fmt.Errorf("clear publication references: %w", err)
		}
		now := s.now().UTC()
		if head == nil {
			head = &model.ReferenceHead{RefType: publicationReferenceType, RefID: command.DocumentID, OwnerID: command.OwnerID, UpdatedAt: now}
			if err := tx.Create(head).Error; err != nil {
				return fmt.Errorf("create cleared publication reference head: %w", err)
			}
		}
		return tx.Model(head).Updates(map[string]any{
			"owner_id": command.OwnerID, "active_generation": command.Generation,
			"active_hash": command.RequestHash, "staged_generation": nil,
			"staged_hash": "", "updated_at": now,
		}).Error
	})
}

func (s *Store) PublicationReferenceState(ctx context.Context, documentID string) (*PublicationReferenceState, error) {
	var head model.ReferenceHead
	if err := s.db.WithContext(ctx).Where("ref_type = ? AND ref_id = ?", publicationReferenceType, documentID).First(&head).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &PublicationReferenceState{DocumentID: documentID}, nil
		}
		return nil, fmt.Errorf("get publication reference state: %w", err)
	}
	return &PublicationReferenceState{DocumentID: documentID, ActiveGeneration: head.ActiveGeneration, StagedGeneration: head.StagedGeneration}, nil
}

func (s *Store) GetPublished(ctx context.Context, attachmentID string) (*domain.Attachment, error) {
	var record model.Attachment
	err := s.db.WithContext(ctx).
		Joins("JOIN attachment.references r ON r.attachment_id = attachment.attachments.id").
		Joins("JOIN attachment.reference_heads h ON h.ref_type = r.ref_type AND h.ref_id = r.ref_id AND (r.generation = h.active_generation OR r.generation = h.staged_generation)").
		Where("attachment.attachments.id = ? AND attachment.attachments.status = ?", attachmentID, domain.StatusReady).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get published attachment: %w", err)
	}
	return toDomain(&record), nil
}

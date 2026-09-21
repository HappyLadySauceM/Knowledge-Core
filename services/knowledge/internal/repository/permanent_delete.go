package repository

import (
	"context"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PurgeDeletedDocument marks a soft-deleted document for immediate physical
// cleanup. The worker performs the cross-service cleanup; keeping the marker in
// Knowledge makes retries safe and hides the document from the trash at once.
func (s *Store) PurgeDeletedDocument(ctx context.Context, id string, actorID, expected int64, idempotency Idempotency) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			if existingID != id {
				return ErrConflict
			}
			return nil
		}

		var record model.Document
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&record).Error; err != nil {
			return mapNotFound("lock document purge request", err)
		}
		access, err := accessFor(tx, &record, actorID)
		if err != nil {
			return err
		}
		if access != domain.AccessOwner || record.DeletedAt == nil {
			return ErrForbidden
		}
		if record.MetadataRevision != expected {
			return ErrPrecondition
		}
		now := s.now().UTC()
		if record.PurgeAfter != nil && !record.PurgeAfter.After(now) {
			return ErrGone
		}
		if err := tx.Model(&record).Updates(map[string]any{
			"purge_after":       now,
			"metadata_revision": gorm.Expr("metadata_revision + 1"),
			"updated_at":        now,
		}).Error; err != nil {
			return fmt.Errorf("mark document for permanent deletion: %w", err)
		}
		if err := s.saveIdempotency(tx, idempotency, id); err != nil {
			return err
		}
		return nil
	})
}

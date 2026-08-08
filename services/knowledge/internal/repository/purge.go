package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PurgeCandidate struct {
	DocumentID string
	ObjectKeys []string
}

func (s *Store) ListPurgeCandidates(ctx context.Context, limit int) ([]PurgeCandidate, error) {
	if limit <= 0 {
		limit = 20
	}
	var documents []model.Document
	if err := s.db.WithContext(ctx).Where("purge_after IS NOT NULL AND purge_after <= ?", s.now().UTC()).
		Order("purge_after ASC, id ASC").Limit(limit).Find(&documents).Error; err != nil {
		return nil, fmt.Errorf("list documents due for purge: %w", err)
	}
	result := make([]PurgeCandidate, 0, len(documents))
	for index := range documents {
		var keys []string
		if err := s.db.WithContext(ctx).Model(&model.Attachment{}).Where("document_id = ?", documents[index].ID).
			Pluck("object_key", &keys).Error; err != nil {
			return nil, fmt.Errorf("list purge attachment objects: %w", err)
		}
		result = append(result, PurgeCandidate{DocumentID: documents[index].ID, ObjectKeys: keys})
	}
	return result, nil
}

func (s *Store) PurgeDocument(ctx context.Context, documentID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var document model.Document
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", documentID).First(&document).Error; err != nil {
			return mapNotFound("lock document purge", err)
		}
		now := s.now().UTC()
		if document.PurgeAfter == nil || document.PurgeAfter.After(now) {
			return ErrPrecondition
		}
		if err := tx.Model(&model.SlugAlias{}).Where("document_id = ?", documentID).
			Updates(map[string]any{"document_id": nil, "gone_at": now}).Error; err != nil {
			return fmt.Errorf("tombstone purged document slugs: %w", err)
		}
		if err := tx.Delete(&document).Error; err != nil {
			return fmt.Errorf("purge document: %w", err)
		}
		return nil
	})
}

func (s *Store) PurgeMaintenanceData(ctx context.Context) error {
	now := s.now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&model.IdempotencyKey{}).Error; err != nil {
			return fmt.Errorf("purge expired idempotency keys: %w", err)
		}
		if err := tx.Where("published_at IS NOT NULL AND published_at < ?", now.Add(-7*24*time.Hour)).Delete(&model.Outbox{}).Error; err != nil {
			return fmt.Errorf("purge published outbox messages: %w", err)
		}
		return nil
	})
}

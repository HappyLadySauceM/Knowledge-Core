package repository

import (
	"context"
	"fmt"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type permissionChangedEvent struct {
	DocumentID         string `json:"document_id"`
	PermissionRevision int64  `json:"permission_revision"`
	Deleted            bool   `json:"deleted"`
}

func (s *Store) enqueuePermissionChanged(tx *gorm.DB, document *model.Document, now time.Time) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	payload, err := jsoncodec.Marshal(permissionChangedEvent{
		DocumentID: document.ID, PermissionRevision: document.PermissionRevision,
		Deleted: document.DeletedAt != nil,
	})
	if err != nil {
		return fmt.Errorf("encode permission change event: %w", err)
	}
	record := model.Outbox{
		ID: id, Subject: permissionChangedSubject, Payload: payload,
		NextAttemptAt: now, CreatedAt: now,
	}
	if err := tx.Create(&record).Error; err != nil {
		return fmt.Errorf("enqueue permission change event: %w", err)
	}
	return nil
}

func (s *Store) ClaimOutbox(ctx context.Context, limit int, lease time.Duration) ([]domain.OutboxMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	var result []domain.OutboxMessage
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []model.Outbox
		now := s.now().UTC()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("published_at IS NULL AND next_attempt_at <= ?", now).
			Order("next_attempt_at ASC, created_at ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
			return fmt.Errorf("claim outbox messages: %w", err)
		}
		for index := range records {
			if err := tx.Model(&records[index]).Updates(map[string]any{
				"attempts": gorm.Expr("attempts + 1"), "next_attempt_at": now.Add(lease),
			}).Error; err != nil {
				return fmt.Errorf("lease outbox message: %w", err)
			}
			result = append(result, domain.OutboxMessage{
				ID: records[index].ID, Subject: records[index].Subject,
				Payload: append([]byte(nil), records[index].Payload...), Attempts: records[index].Attempts + 1,
			})
		}
		return nil
	})
	return result, err
}

func (s *Store) MarkOutboxPublished(ctx context.Context, id string) error {
	result := s.db.WithContext(ctx).Model(&model.Outbox{}).Where("id = ? AND published_at IS NULL", id).
		Update("published_at", s.now().UTC())
	if result.Error != nil {
		return fmt.Errorf("mark outbox message published: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RetryOutbox(ctx context.Context, id string, delay time.Duration) error {
	result := s.db.WithContext(ctx).Model(&model.Outbox{}).Where("id = ? AND published_at IS NULL", id).
		Update("next_attempt_at", s.now().UTC().Add(delay))
	if result.Error != nil {
		return fmt.Errorf("schedule outbox retry: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

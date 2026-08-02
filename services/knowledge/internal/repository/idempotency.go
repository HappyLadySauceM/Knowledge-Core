package repository

import (
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
)

func (s *Store) idempotentResource(tx *gorm.DB, value Idempotency) (string, bool, error) {
	if value.Key == "" {
		return "", false, nil
	}
	lockKey := fmt.Sprintf("%d:%s:%s", value.ActorID, value.Operation, value.Key)
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
		return "", false, fmt.Errorf("lock idempotency key: %w", err)
	}
	var record model.IdempotencyKey
	err := tx.Where("actor_id = ? AND operation = ? AND key = ?", value.ActorID, value.Operation, value.Key).First(&record).Error
	if err == nil {
		if !record.ExpiresAt.After(s.now().UTC()) {
			if deleteErr := tx.Delete(&record).Error; deleteErr != nil {
				return "", false, fmt.Errorf("expire idempotency key: %w", deleteErr)
			}
			return "", false, nil
		}
		if record.RequestHash != value.RequestHash {
			return "", false, ErrConflict
		}
		return record.ResourceID, true, nil
	}
	if err != gorm.ErrRecordNotFound {
		return "", false, fmt.Errorf("read idempotency key: %w", err)
	}
	return "", false, nil
}

func (s *Store) saveIdempotency(tx *gorm.DB, value Idempotency, resourceID string) error {
	if value.Key == "" {
		return nil
	}
	now := s.now().UTC()
	record := model.IdempotencyKey{
		ActorID: value.ActorID, Operation: value.Operation, Key: value.Key, ResourceID: resourceID,
		RequestHash: value.RequestHash, ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
	}
	if err := tx.Create(&record).Error; err != nil {
		return mapWriteError("save idempotency key", err)
	}
	return nil
}

package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type EmailOutboxRepository interface {
	Enqueue(context.Context, domain.EmailMessage) error
	Claim(context.Context, time.Time) (*model.EmailOutbox, error)
	MarkSent(context.Context, int64, time.Time) error
	MarkFailed(context.Context, int64, string, time.Time, time.Duration) error
}

type postgresEmailOutboxRepository struct {
	db  *gorm.DB
	key string
}

func NewEmailOutboxRepository(db *gorm.DB, key string) (EmailOutboxRepository, error) {
	if db == nil || len(key) < 16 {
		return nil, errors.New("create email outbox repository: database and encryption key are required")
	}
	return &postgresEmailOutboxRepository{db: db, key: key}, nil
}

func (r *postgresEmailOutboxRepository) Enqueue(ctx context.Context, message domain.EmailMessage) error {
	ciphertext, err := security.SealEmailToken(message.Token, r.key)
	if err != nil {
		return fmt.Errorf("seal email token: %w", err)
	}
	record := &model.EmailOutbox{Kind: message.Kind, Recipient: message.To, Subject: message.Subject, TokenCipher: ciphertext, NextAttemptAt: message.CreatedAt.UTC(), CreatedAt: message.CreatedAt.UTC()}
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("enqueue identity email: %w", err)
	}
	return nil
}

func (r *postgresEmailOutboxRepository) Claim(ctx context.Context, now time.Time) (*model.EmailOutbox, error) {
	var record model.EmailOutbox
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("sent_at IS NULL AND next_attempt_at <= ?", now.UTC()).Order("created_at ASC").First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOutboxEmpty
			}
			return err
		}
		return tx.Model(&record).Updates(map[string]any{"attempts": gorm.Expr("attempts + 1"), "next_attempt_at": now.UTC().Add(5 * time.Minute)}).Error
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *postgresEmailOutboxRepository) MarkSent(ctx context.Context, id int64, at time.Time) error {
	return r.db.WithContext(ctx).Model(&model.EmailOutbox{}).Where("id = ?", id).Updates(map[string]any{"sent_at": at.UTC(), "last_error": ""}).Error
}
func (r *postgresEmailOutboxRepository) MarkFailed(ctx context.Context, id int64, message string, now time.Time, delay time.Duration) error {
	return r.db.WithContext(ctx).Model(&model.EmailOutbox{}).Where("id = ?", id).Updates(map[string]any{"last_error": message, "next_attempt_at": now.UTC().Add(delay)}).Error
}

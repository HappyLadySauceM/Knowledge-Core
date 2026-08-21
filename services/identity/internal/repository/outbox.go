package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"github.com/google/uuid"
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
	record := &model.EmailOutbox{MessageID: uuid.NewString(), SchemaVersion: 1, Kind: message.Kind, Locale: "zh-CN", Recipient: message.To, Subject: message.Subject, TokenCipher: ciphertext, Status: "pending", NextAttemptAt: message.CreatedAt.UTC(), CreatedAt: message.CreatedAt.UTC(), UpdatedAt: message.CreatedAt.UTC()}
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("enqueue identity email: %w", err)
	}
	return nil
}

func (r *postgresEmailOutboxRepository) Claim(ctx context.Context, now time.Time) (*model.EmailOutbox, error) {
	var record model.EmailOutbox
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("sent_at IS NULL AND parked_at IS NULL AND next_attempt_at <= ? AND (status = 'pending' OR (status = 'sending' AND lease_until IS NOT NULL AND lease_until < ?))", now.UTC(), now.UTC()).Order("created_at ASC").First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOutboxEmpty
			}
			return err
		}
		leaseUntil := now.UTC().Add(90 * time.Second)
		if record.MessageID == "" {
			record.MessageID = uuid.NewString()
		}
		if err := tx.Model(&record).Updates(map[string]any{"message_id": record.MessageID, "attempts": gorm.Expr("attempts + 1"), "status": "sending", "lease_owner": uuid.NewString(), "lease_until": leaseUntil, "next_attempt_at": leaseUntil, "updated_at": now.UTC()}).Error; err != nil {
			return err
		}
		record.Attempts++
		record.Status = "sending"
		record.LeaseUntil = &leaseUntil
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (r *postgresEmailOutboxRepository) MarkSent(ctx context.Context, id int64, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.EmailOutbox{}).Where("id = ? AND sent_at IS NULL AND parked_at IS NULL", id).Updates(map[string]any{"status": "sent", "sent_at": at.UTC(), "lease_owner": "", "lease_until": nil, "last_error": "", "last_error_code": "", "updated_at": at.UTC()})
	if result.Error != nil {
		return fmt.Errorf("mark identity email sent: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return errors.New("mark identity email sent: outbox row is no longer pending")
	}
	return nil
}
func (r *postgresEmailOutboxRepository) MarkFailed(ctx context.Context, id int64, message string, now time.Time, delay time.Duration) error {
	var record model.EmailOutbox
	if err := r.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		return fmt.Errorf("load identity email failure: %w", err)
	}
	status := "pending"
	var parkedAt *time.Time
	if record.Attempts >= 8 {
		status = "parked"
		value := now.UTC()
		parkedAt = &value
	}
	result := r.db.WithContext(ctx).Model(&record).Updates(map[string]any{"status": status, "parked_at": parkedAt, "last_error": message, "last_error_code": "smtp_send_failed", "lease_owner": "", "lease_until": nil, "next_attempt_at": now.UTC().Add(delay), "updated_at": now.UTC()})
	if result.Error != nil {
		return fmt.Errorf("mark identity email failed: %w", result.Error)
	}
	return nil
}

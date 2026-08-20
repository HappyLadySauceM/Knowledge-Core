package repository

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrActionNotFound = errors.New("identity repository: action token not found")
var ErrOutboxEmpty = errors.New("identity repository: email outbox empty")

type ActionRepository interface {
	Create(context.Context, *domain.ActionToken) error
	Consume(context.Context, string, []byte, time.Time) (*domain.ActionToken, error)
}

type postgresActionRepository struct {
	db  *gorm.DB
	key string
}

func NewActionRepository(db *gorm.DB, key ...string) (ActionRepository, error) {
	if db == nil {
		return nil, errors.New("create postgres action repository: database is required")
	}
	secret := ""
	if len(key) > 0 {
		secret = key[0]
	}
	return &postgresActionRepository{db: db, key: secret}, nil
}

func (r *postgresActionRepository) CreateAndEnqueue(ctx context.Context, token *domain.ActionToken, message domain.EmailMessage) error {
	if r.key == "" {
		return errors.New("create identity action email: encryption key is required")
	}
	ciphertext, err := security.SealEmailToken(message.Token, r.key)
	if err != nil {
		return err
	}
	now := token.CreatedAt.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&model.ActionToken{ID: token.ID, UserID: token.UserID, Kind: token.Kind, Digest: token.Digest, ExpiresAt: token.ExpiresAt.UTC(), CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("create identity action token: %w", err)
		}
		if err := tx.Create(&model.EmailOutbox{Kind: message.Kind, Recipient: message.To, Subject: message.Subject, TokenCipher: ciphertext, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("enqueue identity email: %w", err)
		}
		return nil
	})
}

func (r *postgresActionRepository) Create(ctx context.Context, token *domain.ActionToken) error {
	if token == nil || token.ID == "" || token.UserID <= 0 || token.Kind == "" || len(token.Digest) == 0 {
		return errors.New("create identity action: token is required")
	}
	record := &model.ActionToken{ID: token.ID, UserID: token.UserID, Kind: token.Kind, Digest: token.Digest, ExpiresAt: token.ExpiresAt.UTC(), UsedAt: token.UsedAt, CreatedAt: token.CreatedAt.UTC()}
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("create identity action token: %w", err)
	}
	return nil
}

func (r *postgresActionRepository) Consume(ctx context.Context, kind string, digest []byte, now time.Time) (*domain.ActionToken, error) {
	var result *domain.ActionToken
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record model.ActionToken
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind = ? AND digest = ? AND used_at IS NULL AND expires_at > ?", kind, digest, now.UTC()).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrActionNotFound
			}
			return fmt.Errorf("find identity action token: %w", err)
		}
		if subtle.ConstantTimeCompare(record.Digest, digest) != 1 || record.UsedAt != nil || !record.ExpiresAt.After(now.UTC()) {
			return ErrActionNotFound
		}
		when := now.UTC()
		if err := tx.Model(&record).Update("used_at", when).Error; err != nil {
			return fmt.Errorf("consume identity action token: %w", err)
		}
		record.UsedAt = &when
		result = &domain.ActionToken{ID: record.ID, UserID: record.UserID, Kind: record.Kind, Digest: record.Digest, ExpiresAt: record.ExpiresAt, UsedAt: record.UsedAt, CreatedAt: record.CreatedAt}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

var _ ActionRepository = (*postgresActionRepository)(nil)

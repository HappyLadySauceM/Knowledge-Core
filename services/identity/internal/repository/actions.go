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
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrActionNotFound    = errors.New("identity repository: action token not found")
	ErrActionExpired     = errors.New("identity repository: action token expired")
	ErrActionAlreadyUsed = errors.New("identity repository: action token already used")
	ErrOutboxEmpty       = errors.New("identity repository: email outbox empty")
)

// classifyActionToken inspects a locked action_tokens row without used_at/expires_at in the lookup.
// 在仅按 kind+digest 加锁命中后，拆开已用与过期，避免一律变成 not found。
func classifyActionToken(action *model.ActionToken, now time.Time) error {
	if action == nil {
		return ErrActionNotFound
	}
	if action.UsedAt != nil {
		return ErrActionAlreadyUsed
	}
	if !action.ExpiresAt.After(now.UTC()) {
		return ErrActionExpired
	}
	return nil
}

func lockActionByDigest(tx *gorm.DB, kind string, digest []byte) (*model.ActionToken, error) {
	var action model.ActionToken
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("kind = ? AND digest = ?", kind, digest).First(&action).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrActionNotFound
		}
		return nil, fmt.Errorf("find identity action token: %w", err)
	}
	if subtle.ConstantTimeCompare(action.Digest, digest) != 1 {
		return nil, ErrActionNotFound
	}
	return &action, nil
}

func consumeLockedAction(tx *gorm.DB, action *model.ActionToken, now time.Time) error {
	if err := classifyActionToken(action, now); err != nil {
		return err
	}
	when := now.UTC()
	if err := tx.Model(action).Update("used_at", when).Error; err != nil {
		return fmt.Errorf("consume identity action token: %w", err)
	}
	action.UsedAt = &when
	return nil
}

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
		if err := tx.Where("user_id = ? AND kind = ? AND used_at IS NULL AND expires_at > ?", token.UserID, token.Kind, now).Delete(&model.ActionToken{}).Error; err != nil {
			return fmt.Errorf("invalidate previous identity action tokens: %w", err)
		}
		if err := tx.Create(&model.ActionToken{ID: token.ID, UserID: token.UserID, Kind: token.Kind, Digest: token.Digest, ExpiresAt: token.ExpiresAt.UTC(), CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("create identity action token: %w", err)
		}
		if err := tx.Create(&model.EmailOutbox{MessageID: uuid.NewString(), SchemaVersion: 1, Kind: message.Kind, Locale: "zh-CN", Recipient: message.To, Subject: message.Subject, TokenCipher: ciphertext, Status: "pending", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return fmt.Errorf("enqueue identity email: %w", err)
		}
		return nil
	})
}

func (r *postgresActionRepository) CreateUserAndEnqueue(ctx context.Context, user *domain.User, token *domain.ActionToken, message domain.EmailMessage) error {
	if user == nil || token == nil {
		return errors.New("create identity user and email: user and token are required")
	}
	if r.key == "" {
		return errors.New("create identity user and email: encryption key is required")
	}
	ciphertext, err := security.SealEmailToken(message.Token, r.key)
	if err != nil {
		return err
	}
	now := token.CreatedAt.UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record := toModel(user)
		if err := tx.Create(record).Error; err != nil {
			return mapCreateError(err)
		}
		applyModel(user, record)
		token.UserID = user.ID
		if err := tx.Create(&model.ActionToken{ID: token.ID, UserID: token.UserID, Kind: token.Kind, Digest: token.Digest, ExpiresAt: token.ExpiresAt.UTC(), CreatedAt: now}).Error; err != nil {
			return fmt.Errorf("create identity action token: %w", err)
		}
		if err := tx.Create(&model.EmailOutbox{MessageID: uuid.NewString(), SchemaVersion: 1, Kind: message.Kind, Locale: "zh-CN", Recipient: message.To, Subject: message.Subject, TokenCipher: ciphertext, Status: "pending", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return fmt.Errorf("enqueue identity email: %w", err)
		}
		return nil
	})
}

func (r *postgresActionRepository) ConsumeAndVerifyEmail(ctx context.Context, digest []byte, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		action, err := lockActionByDigest(tx, domain.ActionEmailVerification, digest)
		if err != nil {
			return err
		}
		if err := consumeLockedAction(tx, action, now); err != nil {
			return err
		}
		when := now.UTC()
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", action.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return fmt.Errorf("lock identity user for verification: %w", err)
		}
		if user.EmailVerifiedAt == nil {
			if err := tx.Model(&user).Updates(map[string]any{"email_verified_at": when, "status": domain.StatusActive, "updated_at": when}).Error; err != nil {
				return fmt.Errorf("mark identity email verified: %w", err)
			}
		}
		return nil
	})
}

func (r *postgresActionRepository) ConsumeAndResetPassword(ctx context.Context, digest []byte, passwordHash string, now time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		action, err := lockActionByDigest(tx, domain.ActionPasswordReset, digest)
		if err != nil {
			return err
		}
		if err := consumeLockedAction(tx, action, now); err != nil {
			return err
		}
		when := now.UTC()
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", action.UserID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return fmt.Errorf("lock identity user for password reset: %w", err)
		}
		if err := tx.Model(&user).Updates(map[string]any{"password_hash": passwordHash, "token_version": gorm.Expr("token_version + 1"), "failed_login_attempts": 0, "locked_until": nil, "updated_at": when}).Error; err != nil {
			return fmt.Errorf("update identity password: %w", err)
		}
		if err := tx.Model(&model.Session{}).Where("user_id = ? AND revoked_at IS NULL", action.UserID).Updates(map[string]any{"revoked_at": when, "revoked_reason": "password_reset"}).Error; err != nil {
			return fmt.Errorf("revoke identity sessions after password reset: %w", err)
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
		record, err := lockActionByDigest(tx, kind, digest)
		if err != nil {
			return err
		}
		if err := consumeLockedAction(tx, record, now); err != nil {
			return err
		}
		result = &domain.ActionToken{ID: record.ID, UserID: record.UserID, Kind: record.Kind, Digest: record.Digest, ExpiresAt: record.ExpiresAt, UsedAt: record.UsedAt, CreatedAt: record.CreatedAt}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

var _ ActionRepository = (*postgresActionRepository)(nil)

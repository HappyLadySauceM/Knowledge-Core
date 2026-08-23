package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	usernameConstraint = "users_username_lower_uidx"
	emailConstraint    = "users_email_lower_uidx"
)

var (
	ErrUserNotFound     = errors.New("identity repository: user not found")
	ErrUsernameConflict = errors.New("identity repository: username already exists")
	ErrEmailConflict    = errors.New("identity repository: email already exists")
)

type UserRepository interface {
	Create(context.Context, *domain.User) error
	FindByID(context.Context, int64) (*domain.User, error)
	FindByUsername(context.Context, string) (*domain.User, error)
	FindByLogin(context.Context, string) (*domain.User, error)
	RecordLoginFailure(context.Context, int64, time.Time, time.Time, int) (bool, error)
	CompleteLoginSuccess(context.Context, int64, time.Time) (*domain.User, error)
	MarkEmailVerified(context.Context, int64, time.Time) (*domain.User, error)
	UpdatePassword(context.Context, int64, string, time.Time) (*domain.User, error)
	Deactivate(context.Context, int64, time.Time) error
}

func (r *postgresUserRepository) MarkEmailVerified(ctx context.Context, id int64, verifiedAt time.Time) (*domain.User, error) {
	var record model.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return fmt.Errorf("lock identity user for email verification: %w", err)
		}
		if record.EmailVerifiedAt == nil {
			when := verifiedAt.UTC()
			if err := tx.Model(&record).Updates(map[string]any{"email_verified_at": when, "status": domain.StatusActive, "updated_at": when}).Error; err != nil {
				return fmt.Errorf("mark identity email verified: %w", err)
			}
			record.EmailVerifiedAt = &when
			record.Status = domain.StatusActive
			record.UpdatedAt = when
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fromModel(&record), nil
}

func (r *postgresUserRepository) UpdatePassword(ctx context.Context, id int64, passwordHash string, updatedAt time.Time) (*domain.User, error) {
	var record model.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return fmt.Errorf("lock identity user for password reset: %w", err)
		}
		if err := tx.Model(&record).Updates(map[string]any{"password_hash": passwordHash, "token_version": gorm.Expr("token_version + 1"), "failed_login_attempts": 0, "locked_until": nil, "updated_at": updatedAt.UTC()}).Error; err != nil {
			return fmt.Errorf("update identity password: %w", err)
		}
		record.PasswordHash = passwordHash
		record.TokenVersion++
		record.FailedLoginAttempts = 0
		record.LockedUntil = nil
		record.UpdatedAt = updatedAt.UTC()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fromModel(&record), nil
}

func (r *postgresUserRepository) Deactivate(ctx context.Context, id int64, at time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.User{}).Where("id = ? AND status <> ?", id, domain.StatusDisabled).Updates(map[string]any{"status": domain.StatusDisabled, "token_version": gorm.Expr("token_version + 1"), "updated_at": at.UTC()})
	if result.Error != nil {
		return fmt.Errorf("deactivate identity user: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// DeactivateAndRevoke performs the account state transition and session
// invalidation in one Identity database transaction.
func (r *postgresUserRepository) DeactivateAndRevoke(ctx context.Context, id int64, at time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).Where("id = ? AND status <> ?", id, domain.StatusDisabled).Updates(map[string]any{"status": domain.StatusDisabled, "token_version": gorm.Expr("token_version + 1"), "updated_at": at.UTC()})
		if result.Error != nil {
			return fmt.Errorf("deactivate identity user: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return ErrUserNotFound
		}
		if err := tx.Model(&model.Session{}).Where("user_id = ? AND revoked_at IS NULL", id).Updates(map[string]any{"revoked_at": at.UTC(), "revoked_reason": "account_deactivated"}).Error; err != nil {
			return fmt.Errorf("revoke deactivated identity sessions: %w", err)
		}
		return nil
	})
}

func (r *postgresUserRepository) FindByUsername(ctx context.Context, username string) (*domain.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var record model.User
	if err := r.db.WithContext(ctx).Where("lower(username) = ?", username).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("find identity user by username: %w", err)
	}
	return fromModel(&record), nil
}

func (r *postgresUserRepository) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	var record model.User
	if err := r.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("find identity user by ID: %w", err)
	}
	return fromModel(&record), nil
}

func (r *postgresUserRepository) FindByLogin(ctx context.Context, identifier string) (*domain.User, error) {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	var record model.User
	err := r.db.WithContext(ctx).
		Where("lower(username) = ? OR lower(email) = ?", identifier, identifier).
		First(&record).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("find identity user by login: %w", err)
	}
	return fromModel(&record), nil
}

func (r *postgresUserRepository) RecordLoginFailure(
	ctx context.Context,
	id int64,
	failedAt time.Time,
	lockUntil time.Time,
	threshold int,
) (bool, error) {
	var locked bool
	result := r.db.WithContext(ctx).Raw(`
UPDATE identity.users
SET failed_login_attempts = CASE
		WHEN locked_until IS NOT NULL AND locked_until > ? THEN failed_login_attempts
        WHEN locked_until IS NOT NULL AND locked_until <= ? THEN 1
        ELSE failed_login_attempts + 1
    END,
    locked_until = CASE
		WHEN locked_until IS NOT NULL AND locked_until > ? THEN locked_until
        WHEN (CASE
            WHEN locked_until IS NOT NULL AND locked_until <= ? THEN 1
            ELSE failed_login_attempts + 1
        END) >= ? THEN ?
        ELSE NULL
    END,
    updated_at = ?
WHERE id = ?
RETURNING locked_until IS NOT NULL AND locked_until > ?`,
		failedAt, failedAt, failedAt, failedAt, threshold, lockUntil, failedAt, id, failedAt,
	).Scan(&locked)
	if result.Error != nil {
		return false, fmt.Errorf("record identity login failure: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return false, ErrUserNotFound
	}
	return locked, nil
}

func (r *postgresUserRepository) CompleteLoginSuccess(
	ctx context.Context,
	id int64,
	completedAt time.Time,
) (*domain.User, error) {
	var user *domain.User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record model.User
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock identity user for login: %w", err)
		}
		if record.Status != domain.StatusActive || (record.LockedUntil != nil && record.LockedUntil.After(completedAt.UTC())) {
			user = fromModel(&record)
			return nil
		}
		if err := tx.Model(&record).Updates(map[string]any{
			"failed_login_attempts": 0,
			"locked_until":          nil,
			"updated_at":            completedAt.UTC(),
		}).Error; err != nil {
			return fmt.Errorf("complete identity login success: %w", err)
		}
		record.FailedLoginAttempts = 0
		record.LockedUntil = nil
		record.UpdatedAt = completedAt.UTC()
		user = fromModel(&record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

type postgresUserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) (UserRepository, error) {
	if db == nil {
		return nil, errors.New("create postgres user repository: database is required")
	}
	return &postgresUserRepository{db: db}, nil
}

func (r *postgresUserRepository) Create(ctx context.Context, user *domain.User) error {
	if user == nil {
		return errors.New("create postgres user: user is required")
	}
	record := toModel(user)
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return mapCreateError(err)
	}
	applyModel(user, record)
	return nil
}

func mapCreateError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		switch postgresError.ConstraintName {
		case usernameConstraint:
			return ErrUsernameConflict
		case emailConstraint:
			return ErrEmailConflict
		}
	}
	return fmt.Errorf("insert identity user: %w", err)
}

func toModel(user *domain.User) *model.User {
	return &model.User{
		ID:                  user.ID,
		Username:            user.Username,
		Email:               user.Email,
		PasswordHash:        user.PasswordHash,
		Role:                user.Role,
		Status:              user.Status,
		TokenVersion:        user.TokenVersion,
		Avatar:              user.Avatar,
		AvatarAttachmentID:  user.AvatarAttachmentID,
		Bio:                 user.Bio,
		FailedLoginAttempts: user.FailedLoginAttempts,
		LockedUntil:         user.LockedUntil,
		EmailVerifiedAt:     user.EmailVerifiedAt,
		CreatedAt:           user.CreatedAt,
		UpdatedAt:           user.UpdatedAt,
	}
}

func applyModel(user *domain.User, record *model.User) {
	*user = *fromModel(record)
}

func fromModel(record *model.User) *domain.User {
	if record == nil {
		return nil
	}
	return &domain.User{
		ID: record.ID, Username: record.Username, Email: record.Email,
		PasswordHash: record.PasswordHash, Role: record.Role, Status: record.Status,
		TokenVersion: record.TokenVersion, Avatar: record.Avatar, AvatarAttachmentID: record.AvatarAttachmentID, Bio: record.Bio,
		FailedLoginAttempts: record.FailedLoginAttempts, LockedUntil: record.LockedUntil,
		EmailVerifiedAt: record.EmailVerifiedAt,
		CreatedAt:       record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

var _ UserRepository = (*postgresUserRepository)(nil)

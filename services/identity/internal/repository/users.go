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
	FindByLogin(context.Context, string) (*domain.User, error)
	RecordLoginFailure(context.Context, int64, time.Time, time.Time, int) (bool, error)
	CompleteLoginSuccess(context.Context, int64, time.Time) (*domain.User, error)
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
		Bio:                 user.Bio,
		FailedLoginAttempts: user.FailedLoginAttempts,
		LockedUntil:         user.LockedUntil,
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
		TokenVersion: record.TokenVersion, Avatar: record.Avatar, Bio: record.Bio,
		FailedLoginAttempts: record.FailedLoginAttempts, LockedUntil: record.LockedUntil,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

var _ UserRepository = (*postgresUserRepository)(nil)

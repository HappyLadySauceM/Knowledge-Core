package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

const (
	usernameConstraint = "users_username_lower_uidx"
	emailConstraint    = "users_email_lower_uidx"
)

var (
	ErrUsernameConflict = errors.New("identity repository: username already exists")
	ErrEmailConflict    = errors.New("identity repository: email already exists")
)

type UserRepository interface {
	Create(context.Context, *domain.User) error
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
	user.ID = record.ID
	user.CreatedAt = record.CreatedAt
	user.UpdatedAt = record.UpdatedAt
}

var _ UserRepository = (*postgresUserRepository)(nil)

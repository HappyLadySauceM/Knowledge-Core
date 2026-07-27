package repository

import (
	"context"
	"errors"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
)

var (
	ErrUserNotFound     = errors.New("identity repository: user not found")
	ErrUsernameConflict = errors.New("identity repository: username already exists")
	ErrEmailConflict    = errors.New("identity repository: email already exists")
)

type UserRepository interface {
	Create(ctx context.Context, user *domain.User) error
	FindByID(ctx context.Context, id int64) (*domain.User, error)
	FindByLogin(ctx context.Context, identifier string) (*domain.User, error)
	RecordLoginFailure(ctx context.Context, id int64, failedAt, lockUntil time.Time, threshold int) (bool, error)
	RecordLoginSuccess(ctx context.Context, id int64) error
}

package app

import (
	"context"
	"errors"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
)

var (
	ErrUsernameConflict = errors.New("identity: username already exists")
	ErrEmailConflict    = errors.New("identity: email already exists")
)

// UserRepository is the persistence port required by Identity use cases.
// Infrastructure adapters implement it without exposing GORM to this package.
type UserRepository interface {
	Create(context.Context, *domain.User) error
}

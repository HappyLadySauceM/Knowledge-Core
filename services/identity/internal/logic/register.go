package logic

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

type PasswordHasher interface {
	Hash(string) (string, error)
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type RegisterLogic struct {
	users  repository.UserRepository
	hasher PasswordHasher
}

func NewRegisterLogic(users repository.UserRepository, hasher PasswordHasher) (*RegisterLogic, error) {
	if users == nil || hasher == nil {
		return nil, errors.New("create identity register logic: user repository and password hasher are required")
	}
	return &RegisterLogic{users: users, hasher: hasher}, nil
}

func (l *RegisterLogic) Register(ctx context.Context, input RegisterInput) (*domain.User, error) {
	if err := domain.ValidatePassword(input.Password); err != nil {
		return nil, identityerrors.InvalidInput.Wrap(err)
	}
	user, err := domain.NewUser(input.Username, input.Email)
	if err != nil {
		return nil, identityerrors.InvalidInput.Wrap(err)
	}
	user.PasswordHash, err = l.hasher.Hash(input.Password)
	if err != nil {
		return nil, fmt.Errorf("hash identity password: %w", err)
	}
	if err := l.users.Create(ctx, user); err != nil {
		switch {
		case errors.Is(err, repository.ErrUsernameConflict):
			return nil, identityerrors.UsernameConflict.Wrap(err)
		case errors.Is(err, repository.ErrEmailConflict):
			return nil, identityerrors.EmailConflict.Wrap(err)
		default:
			return nil, fmt.Errorf("register identity user: %w", err)
		}
	}
	return user, nil
}

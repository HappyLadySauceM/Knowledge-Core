package logic

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

type getUserRepository interface {
	FindByID(context.Context, int64) (*domain.User, error)
	FindByUsername(context.Context, string) (*domain.User, error)
}

func (l *GetUserLogic) ResolveUser(ctx context.Context, username string) (*domain.User, error) {
	if err := domain.ValidateUsername(username); err != nil {
		return nil, identityerrors.InvalidInput.Wrap(err)
	}
	user, err := l.users.FindByUsername(ctx, username)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, identityerrors.UserNotFound.Wrap(err)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve identity user: %w", err)
	}
	return user, nil
}

type GetUserLogic struct {
	users getUserRepository
}

func NewGetUserLogic(users getUserRepository) (*GetUserLogic, error) {
	if users == nil {
		return nil, errors.New("create identity get-user logic: users are required")
	}
	return &GetUserLogic{users: users}, nil
}

func (l *GetUserLogic) GetUser(ctx context.Context, id int64) (*domain.User, error) {
	if id <= 0 {
		return nil, identityerrors.InvalidInput.Wrap(&domain.ValidationError{Field: "id", Reason: "must be positive"})
	}
	user, err := l.users.FindByID(ctx, id)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, identityerrors.UserNotFound.Wrap(err)
	}
	if err != nil {
		return nil, fmt.Errorf("get identity user: %w", err)
	}
	return user, nil
}

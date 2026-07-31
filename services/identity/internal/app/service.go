package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
)

type PasswordHasher interface {
	Hash(string) (string, error)
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type Service struct {
	users  UserRepository
	hasher PasswordHasher
}

func NewService(users UserRepository, hasher PasswordHasher) (*Service, error) {
	if users == nil || hasher == nil {
		return nil, errors.New("create identity service: user repository and password hasher are required")
	}
	return &Service{users: users, hasher: hasher}, nil
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (*domain.User, error) {
	if err := domain.ValidatePassword(input.Password); err != nil {
		return nil, err
	}
	user, err := domain.NewUser(input.Username, input.Email)
	if err != nil {
		return nil, err
	}
	user.PasswordHash, err = s.hasher.Hash(input.Password)
	if err != nil {
		return nil, fmt.Errorf("hash identity password: %w", err)
	}
	if err := s.users.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("register identity user: %w", err)
	}
	return user, nil
}

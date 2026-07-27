package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

const (
	loginFailureThreshold = 5
	loginLockDuration     = 15 * time.Minute
)

var (
	ErrInvalidCredentials = errors.New("identity: invalid credentials")
	ErrAccountLocked      = errors.New("identity: account is temporarily locked")
	ErrUserDisabled       = errors.New("identity: user is disabled")
	ErrUserNotFound       = errors.New("identity: user not found")
	ErrUsernameConflict   = errors.New("identity: username already exists")
	ErrEmailConflict      = errors.New("identity: email already exists")
)

type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) (bool, error)
}

type RegisterInput struct {
	Username string
	Email    string
	Password string
}

type AuthenticateInput struct {
	Identifier string
	Password   string
}

type Service struct {
	users     repository.UserRepository
	hasher    PasswordHasher
	dummyHash string
	now       func() time.Time
}

func NewService(users repository.UserRepository, hasher PasswordHasher) (*Service, error) {
	if users == nil || hasher == nil {
		return nil, errors.New("create identity service: user repository and password hasher are required")
	}
	dummyHash, err := hasher.Hash("identity-dummy-password")
	if err != nil {
		return nil, fmt.Errorf("create identity service dummy password: %w", err)
	}
	return &Service{users: users, hasher: hasher, dummyHash: dummyHash, now: time.Now}, nil
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
		return nil, err
	}
	if err := s.users.Create(ctx, user); err != nil {
		switch {
		case errors.Is(err, repository.ErrUsernameConflict):
			return nil, ErrUsernameConflict
		case errors.Is(err, repository.ErrEmailConflict):
			return nil, ErrEmailConflict
		default:
			return nil, fmt.Errorf("register user: %w", err)
		}
	}
	return user, nil
}

func (s *Service) Authenticate(ctx context.Context, input AuthenticateInput) (*domain.User, error) {
	identifier := strings.TrimSpace(input.Identifier)
	if identifier == "" || len(identifier) > 320 || input.Password == "" || len([]byte(input.Password)) > 72 {
		return nil, ErrInvalidCredentials
	}
	user, err := s.users.FindByLogin(ctx, identifier)
	if errors.Is(err, repository.ErrUserNotFound) {
		if _, compareErr := s.hasher.Compare(s.dummyHash, input.Password); compareErr != nil {
			return nil, compareErr
		}
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, fmt.Errorf("authenticate user: %w", err)
	}
	now := s.now().UTC()
	matched, err := s.hasher.Compare(user.PasswordHash, input.Password)
	if err != nil {
		return nil, err
	}
	if !matched {
		if user.Status != domain.StatusActive || user.IsLocked(now) {
			return nil, ErrInvalidCredentials
		}
		locked, recordErr := s.users.RecordLoginFailure(ctx, user.ID, now, now.Add(loginLockDuration), loginFailureThreshold)
		if recordErr != nil {
			return nil, fmt.Errorf("record login failure: %w", recordErr)
		}
		if locked {
			return nil, ErrAccountLocked
		}
		return nil, ErrInvalidCredentials
	}
	if user.Status != domain.StatusActive {
		return nil, ErrUserDisabled
	}
	if user.IsLocked(now) {
		return nil, ErrAccountLocked
	}
	if err := s.users.RecordLoginSuccess(ctx, user.ID); err != nil {
		return nil, fmt.Errorf("record login success: %w", err)
	}
	return user, nil
}

func (s *Service) GetUser(ctx context.Context, id int64) (*domain.User, error) {
	if id <= 0 {
		return nil, &domain.ValidationError{Field: "id", Reason: "must be positive"}
	}
	user, err := s.users.FindByID(ctx, id)
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

func (s *Service) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

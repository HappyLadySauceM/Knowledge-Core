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

type AccessTokenIssuer interface {
	Issue(user *domain.User) (AccessToken, error)
}

type AccessToken struct {
	Value     string
	ExpiresAt time.Time
}

type Authentication struct {
	User        *domain.User
	AccessToken AccessToken
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

type BootstrapAdminInput struct {
	Username string
	Email    string
	Password string
}

type Service struct {
	users       repository.UserRepository
	hasher      PasswordHasher
	tokenIssuer AccessTokenIssuer
	dummyHash   string
	now         func() time.Time
}

func NewService(users repository.UserRepository, hasher PasswordHasher, tokenIssuer AccessTokenIssuer) (*Service, error) {
	if users == nil || hasher == nil || tokenIssuer == nil {
		return nil, errors.New("create identity service: user repository, password hasher, and token issuer are required")
	}
	dummyHash, err := hasher.Hash("identity-dummy-password")
	if err != nil {
		return nil, fmt.Errorf("create identity service dummy password: %w", err)
	}
	return &Service{users: users, hasher: hasher, tokenIssuer: tokenIssuer, dummyHash: dummyHash, now: time.Now}, nil
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

// EnsureBootstrapAdmin creates the first administrator from Secret-provided
// credentials. It never changes an existing administrator.
func (s *Service) EnsureBootstrapAdmin(ctx context.Context, input BootstrapAdminInput) (bool, error) {
	count, err := s.users.CountAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("count bootstrap administrators: %w", err)
	}
	if count > 0 {
		return false, nil
	}
	user, err := domain.NewUser(input.Username, input.Email)
	if err != nil {
		return false, err
	}
	if err := domain.ValidatePassword(input.Password); err != nil {
		return false, err
	}
	user.Role = domain.RoleAdmin
	user.PasswordHash, err = s.hasher.Hash(input.Password)
	if err != nil {
		return false, fmt.Errorf("hash bootstrap administrator password: %w", err)
	}
	created, err := s.users.CreateFirstAdmin(ctx, user)
	if err != nil {
		return false, fmt.Errorf("create bootstrap administrator: %w", err)
	}
	return created, nil
}

func (s *Service) Authenticate(ctx context.Context, input AuthenticateInput) (*Authentication, error) {
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
	user, err = s.users.CompleteLoginSuccess(ctx, user.ID, now)
	if err != nil {
		return nil, fmt.Errorf("record login success: %w", err)
	}
	if user.Status != domain.StatusActive {
		return nil, ErrUserDisabled
	}
	if user.IsLocked(now) {
		return nil, ErrAccountLocked
	}
	accessToken, err := s.tokenIssuer.Issue(user)
	if err != nil {
		return nil, fmt.Errorf("issue access token: %w", err)
	}
	return &Authentication{User: user, AccessToken: accessToken}, nil
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

package logic

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"github.com/google/uuid"
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
	users        repository.UserRepository
	hasher       PasswordHasher
	verification RegistrationRepository
	pepper       string
	ttl          time.Duration
}

type RegistrationRepository interface {
	CreateUserAndEnqueue(context.Context, *domain.User, *domain.ActionToken, domain.EmailMessage) error
}

func NewRegisterLogic(users repository.UserRepository, hasher PasswordHasher, options ...any) (*RegisterLogic, error) {
	if users == nil || hasher == nil {
		return nil, errors.New("create identity register logic: user repository and password hasher are required")
	}
	logic := &RegisterLogic{users: users, hasher: hasher}
	for _, option := range options {
		switch value := option.(type) {
		case RegistrationRepository:
			logic.verification = value
		case string:
			logic.pepper = value
		case time.Duration:
			logic.ttl = value
		}
	}
	if logic.verification != nil && (len(logic.pepper) < 16 || logic.ttl <= 0) {
		return nil, errors.New("create identity register logic: verification pepper and TTL are required")
	}
	return logic, nil
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
	if l.verification != nil {
		token, tokenErr := security.NewActionToken()
		if tokenErr != nil {
			return nil, fmt.Errorf("generate identity verification token: %w", tokenErr)
		}
		now := time.Now().UTC()
		entry := &domain.ActionToken{ID: uuid.NewString(), UserID: user.ID, Kind: domain.ActionEmailVerification, Digest: security.DigestActionToken(token, l.pepper), ExpiresAt: now.Add(l.ttl), CreatedAt: now}
		message := domain.EmailMessage{Kind: domain.ActionEmailVerification, To: user.Email, Subject: "Verify your email", Token: token, CreatedAt: now}
		if err := l.verification.CreateUserAndEnqueue(ctx, user, entry, message); err != nil {
			return nil, fmt.Errorf("register identity user and queue verification: %w", err)
		}
		return user, nil
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

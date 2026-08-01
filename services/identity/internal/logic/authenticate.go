package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

const dummyPassword = "identity-dummy-password-value"

type Authentication struct {
	User        *domain.User
	AccessToken coreauth.IssuedToken
}

type AuthenticateInput struct {
	Identifier string
	Password   string
}

type PasswordVerifier interface {
	Hash(string) (string, error)
	Compare(string, string) (bool, error)
}

type AccessTokenIssuer interface {
	Issue(coreauth.Principal) (coreauth.IssuedToken, error)
}

type authenticateUsers interface {
	FindByLogin(context.Context, string) (*domain.User, error)
	RecordLoginFailure(context.Context, int64, time.Time, time.Time, int) (bool, error)
	CompleteLoginSuccess(context.Context, int64, time.Time) (*domain.User, error)
}

type AuthenticateLogic struct {
	users            authenticateUsers
	passwords        PasswordVerifier
	tokens           AccessTokenIssuer
	dummyHash        string
	failureThreshold int
	lockDuration     time.Duration
	now              func() time.Time
}

func NewAuthenticateLogic(
	users authenticateUsers,
	passwords PasswordVerifier,
	tokens AccessTokenIssuer,
	failureThreshold int,
	lockDuration time.Duration,
) (*AuthenticateLogic, error) {
	if users == nil || passwords == nil || tokens == nil {
		return nil, errors.New("create identity authenticate logic: users, passwords, and tokens are required")
	}
	if failureThreshold < 2 || lockDuration <= 0 {
		return nil, errors.New("create identity authenticate logic: failure threshold and lock duration are invalid")
	}
	dummyHash, err := passwords.Hash(dummyPassword)
	if err != nil {
		return nil, fmt.Errorf("create identity dummy password: %w", err)
	}
	return &AuthenticateLogic{
		users: users, passwords: passwords, tokens: tokens, dummyHash: dummyHash,
		failureThreshold: failureThreshold, lockDuration: lockDuration, now: time.Now,
	}, nil
}

func (l *AuthenticateLogic) Authenticate(ctx context.Context, input AuthenticateInput) (*Authentication, error) {
	identifier := strings.TrimSpace(input.Identifier)
	if identifier == "" || len(identifier) > 320 || input.Password == "" || len([]byte(input.Password)) > 72 {
		return nil, identityerrors.InvalidCredentials.New()
	}
	user, err := l.users.FindByLogin(ctx, identifier)
	if errors.Is(err, repository.ErrUserNotFound) {
		if _, compareErr := l.passwords.Compare(l.dummyHash, input.Password); compareErr != nil {
			return nil, fmt.Errorf("compare identity dummy password: %w", compareErr)
		}
		return nil, identityerrors.InvalidCredentials.Wrap(err)
	}
	if err != nil {
		return nil, fmt.Errorf("find identity login: %w", err)
	}

	now := l.now().UTC()
	matched, err := l.passwords.Compare(user.PasswordHash, input.Password)
	if err != nil {
		return nil, fmt.Errorf("compare identity password: %w", err)
	}
	if user.IsLocked(now) {
		return nil, identityerrors.AccountLocked.New()
	}
	if !matched {
		if user.Status != domain.StatusActive {
			return nil, identityerrors.InvalidCredentials.New()
		}
		locked, recordErr := l.users.RecordLoginFailure(
			ctx, user.ID, now, now.Add(l.lockDuration), l.failureThreshold,
		)
		if recordErr != nil {
			return nil, fmt.Errorf("record identity login failure: %w", recordErr)
		}
		if locked {
			return nil, identityerrors.AccountLocked.New()
		}
		return nil, identityerrors.InvalidCredentials.New()
	}

	user, err = l.users.CompleteLoginSuccess(ctx, user.ID, now)
	if err != nil {
		return nil, fmt.Errorf("complete identity login: %w", err)
	}
	if user == nil {
		return nil, errors.New("complete identity login returned no user")
	}
	if user.Status != domain.StatusActive {
		return nil, identityerrors.UserDisabled.New()
	}
	if user.IsLocked(now) {
		return nil, identityerrors.AccountLocked.New()
	}
	accessToken, err := l.tokens.Issue(coreauth.Principal{
		UserID: user.ID, Role: user.Role, TokenVersion: user.TokenVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("issue identity access token: %w", err)
	}
	return &Authentication{User: user, AccessToken: accessToken}, nil
}

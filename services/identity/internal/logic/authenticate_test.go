package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

type authenticateUsersStub struct {
	user *domain.User
}

func (s *authenticateUsersStub) FindByLogin(context.Context, string) (*domain.User, error) {
	if s.user == nil {
		return nil, repository.ErrUserNotFound
	}
	clone := *s.user
	return &clone, nil
}

func (s *authenticateUsersStub) RecordLoginFailure(_ context.Context, _ int64, _ time.Time, lockUntil time.Time, threshold int) (bool, error) {
	s.user.FailedLoginAttempts++
	if s.user.FailedLoginAttempts >= threshold {
		s.user.LockedUntil = &lockUntil
		return true, nil
	}
	return false, nil
}

func (s *authenticateUsersStub) CompleteLoginSuccess(_ context.Context, _ int64, now time.Time) (*domain.User, error) {
	if s.user.IsLocked(now) || s.user.Status != domain.StatusActive {
		clone := *s.user
		return &clone, nil
	}
	s.user.FailedLoginAttempts = 0
	s.user.LockedUntil = nil
	clone := *s.user
	return &clone, nil
}

type passwordVerifierStub struct{}

func (passwordVerifierStub) Hash(value string) (string, error) { return "hash:" + value, nil }
func (passwordVerifierStub) Compare(hash, value string) (bool, error) {
	return hash == "hash:"+value, nil
}

type tokenIssuerStub struct{}

func (tokenIssuerStub) Issue(principal coreauth.Principal) (coreauth.IssuedToken, error) {
	if principal.UserID <= 0 || principal.TokenVersion <= 0 {
		return coreauth.IssuedToken{}, errors.New("invalid principal")
	}
	return coreauth.IssuedToken{Value: "access-token", ExpiresAt: time.Unix(2000, 0)}, nil
}

func TestAuthenticateLocksAfterFiveFailuresAndUnlocksAfterDuration(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	users := &authenticateUsersStub{user: &domain.User{
		ID: 1, Username: "alice", Email: "alice@example.com", PasswordHash: "hash:correct",
		Role: domain.RoleUser, Status: domain.StatusActive, TokenVersion: 1,
	}}
	logic, err := NewAuthenticateLogic(users, passwordVerifierStub{}, tokenIssuerStub{}, 5, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	logic.now = func() time.Time { return now }

	for attempt := 1; attempt <= 5; attempt++ {
		_, err := logic.Authenticate(context.Background(), AuthenticateInput{Identifier: "alice", Password: "wrong"})
		if attempt < 5 && !errors.Is(err, identityerrors.InvalidCredentials) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
		if attempt == 5 && !errors.Is(err, identityerrors.AccountLocked) {
			t.Fatalf("attempt %d error = %v", attempt, err)
		}
	}
	if users.user.LockedUntil == nil || !users.user.LockedUntil.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("LockedUntil = %v", users.user.LockedUntil)
	}
	if _, err := logic.Authenticate(context.Background(), AuthenticateInput{Identifier: "alice", Password: "correct"}); !errors.Is(err, identityerrors.AccountLocked) {
		t.Fatalf("locked login error = %v", err)
	}
	if _, err := logic.Authenticate(context.Background(), AuthenticateInput{Identifier: "alice", Password: "wrong"}); !errors.Is(err, identityerrors.AccountLocked) {
		t.Fatalf("locked wrong-password error = %v", err)
	}

	logic.now = func() time.Time { return now.Add(15*time.Minute + time.Second) }
	authentication, err := logic.Authenticate(context.Background(), AuthenticateInput{Identifier: "alice", Password: "correct"})
	if err != nil {
		t.Fatalf("unlocked Authenticate() error = %v", err)
	}
	if authentication.AccessToken.Value != "access-token" || users.user.FailedLoginAttempts != 0 || users.user.LockedUntil != nil {
		t.Fatalf("authentication = %#v, user = %#v", authentication, users.user)
	}
}

func TestAuthenticateUnknownUserUsesSafeError(t *testing.T) {
	logic, err := NewAuthenticateLogic(&authenticateUsersStub{}, passwordVerifierStub{}, tokenIssuerStub{}, 5, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = logic.Authenticate(context.Background(), AuthenticateInput{Identifier: "missing", Password: "password"})
	if !errors.Is(err, identityerrors.InvalidCredentials) || apperror.SafeMessage(err) != "invalid credentials" {
		t.Fatalf("Authenticate() error = %v", err)
	}
}

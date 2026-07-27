package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

func TestRegisterHashesPasswordAndMapsConflict(t *testing.T) {
	repositoryFake := &fakeUsers{}
	service, err := app.NewService(repositoryFake, fakeHasher{}, fakeTokenIssuer{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	user, err := service.Register(context.Background(), app.RegisterInput{
		Username: "Alice_01",
		Email:    "ALICE@example.com",
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if user.PasswordHash != "hashed:correct-password" || user.Email != "alice@example.com" || user.ID == 0 {
		t.Fatalf("Register() user = %#v", user)
	}

	repositoryFake.createErr = repository.ErrUsernameConflict
	_, err = service.Register(context.Background(), app.RegisterInput{
		Username: "Alice_02",
		Email:    "alice2@example.com",
		Password: "correct-password",
	})
	if !errors.Is(err, app.ErrUsernameConflict) {
		t.Fatalf("Register() conflict error = %v", err)
	}
}

func TestAuthenticateLocksAfterFiveFailures(t *testing.T) {
	now := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	repositoryFake := &fakeUsers{user: activeUser(now)}
	service, err := app.NewService(repositoryFake, fakeHasher{}, fakeTokenIssuer{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	service.SetClock(func() time.Time { return now })

	for attempt := 1; attempt <= 4; attempt++ {
		_, err = service.Authenticate(context.Background(), app.AuthenticateInput{Identifier: "alice", Password: "wrong"})
		if !errors.Is(err, app.ErrInvalidCredentials) {
			t.Fatalf("Authenticate() attempt %d error = %v", attempt, err)
		}
	}
	_, err = service.Authenticate(context.Background(), app.AuthenticateInput{Identifier: "alice", Password: "wrong"})
	if !errors.Is(err, app.ErrAccountLocked) {
		t.Fatalf("Authenticate() fifth attempt error = %v", err)
	}
	if repositoryFake.user.LockedUntil == nil || !repositoryFake.user.LockedUntil.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("locked until = %v", repositoryFake.user.LockedUntil)
	}
}

func TestAuthenticateResetsFailures(t *testing.T) {
	now := time.Now().UTC()
	repositoryFake := &fakeUsers{user: activeUser(now)}
	repositoryFake.user.FailedLoginAttempts = 2
	service, _ := app.NewService(repositoryFake, fakeHasher{}, fakeTokenIssuer{})
	authentication, err := service.Authenticate(context.Background(), app.AuthenticateInput{
		Identifier: "alice@example.com",
		Password:   "correct-password",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if authentication.User.ID != repositoryFake.user.ID || repositoryFake.user.FailedLoginAttempts != 0 {
		t.Fatalf("Authenticate() result = %#v", authentication)
	}
	if authentication.AccessToken.Value == "" || authentication.AccessToken.ExpiresAt.IsZero() {
		t.Fatalf("Authenticate() token = %#v", authentication.AccessToken)
	}
}

func TestAuthenticateMissingUserStillComparesPassword(t *testing.T) {
	hasher := &recordingHasher{}
	service, err := app.NewService(&fakeUsers{}, hasher, fakeTokenIssuer{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = service.Authenticate(context.Background(), app.AuthenticateInput{
		Identifier: "missing@example.com",
		Password:   "candidate-password",
	})
	if !errors.Is(err, app.ErrInvalidCredentials) {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if hasher.comparisons != 1 {
		t.Fatalf("password comparisons = %d, want 1", hasher.comparisons)
	}
}

func TestAuthenticateDoesNotRevealDisabledUserForWrongPassword(t *testing.T) {
	repositoryFake := &fakeUsers{user: activeUser(time.Now().UTC())}
	repositoryFake.user.Status = domain.StatusDisabled
	service, err := app.NewService(repositoryFake, fakeHasher{}, fakeTokenIssuer{})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	_, err = service.Authenticate(context.Background(), app.AuthenticateInput{
		Identifier: "alice@example.com",
		Password:   "wrong-password",
	})
	if !errors.Is(err, app.ErrInvalidCredentials) {
		t.Fatalf("Authenticate(wrong) error = %v", err)
	}
	_, err = service.Authenticate(context.Background(), app.AuthenticateInput{
		Identifier: "alice@example.com",
		Password:   "correct-password",
	})
	if !errors.Is(err, app.ErrUserDisabled) {
		t.Fatalf("Authenticate(correct) error = %v", err)
	}
}

func activeUser(now time.Time) *domain.User {
	return &domain.User{
		ID:           42,
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: "hashed:correct-password",
		Role:         domain.RoleUser,
		Status:       domain.StatusActive,
		TokenVersion: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

type fakeHasher struct{}

func (fakeHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }

func (fakeHasher) Compare(hash, password string) (bool, error) {
	return hash == "hashed:"+password, nil
}

type recordingHasher struct {
	comparisons int
}

func (*recordingHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }

func (h *recordingHasher) Compare(string, string) (bool, error) {
	h.comparisons++
	return false, nil
}

type fakeTokenIssuer struct {
	err error
}

func (f fakeTokenIssuer) Issue(user *domain.User) (app.AccessToken, error) {
	if f.err != nil {
		return app.AccessToken{}, f.err
	}
	return app.AccessToken{
		Value:     "access-token",
		ExpiresAt: user.UpdatedAt.Add(15 * time.Minute),
	}, nil
}

type fakeUsers struct {
	user      *domain.User
	createErr error
}

func (f *fakeUsers) Create(_ context.Context, user *domain.User) error {
	if f.createErr != nil {
		return f.createErr
	}
	user.ID = 1
	user.CreatedAt = time.Now().UTC()
	user.UpdatedAt = user.CreatedAt
	f.user = user
	return nil
}

func (f *fakeUsers) FindByID(context.Context, int64) (*domain.User, error) {
	if f.user == nil {
		return nil, repository.ErrUserNotFound
	}
	return f.user, nil
}

func (f *fakeUsers) FindByLogin(context.Context, string) (*domain.User, error) {
	if f.user == nil {
		return nil, repository.ErrUserNotFound
	}
	return f.user, nil
}

func (f *fakeUsers) RecordLoginFailure(_ context.Context, _ int64, _ time.Time, lockUntil time.Time, threshold int) (bool, error) {
	f.user.FailedLoginAttempts++
	if f.user.FailedLoginAttempts >= threshold {
		f.user.LockedUntil = &lockUntil
		return true, nil
	}
	return false, nil
}

func (f *fakeUsers) RecordLoginSuccess(context.Context, int64) error {
	f.user.FailedLoginAttempts = 0
	f.user.LockedUntil = nil
	return nil
}

var _ repository.UserRepository = (*fakeUsers)(nil)

package kitex_test

import (
	"context"
	"errors"
	"testing"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	identitykitex "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestPing(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	response, err := identitykitex.NewHandler(nil, nil, registry).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "identity" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

func TestPingReportsNotReadyWhileDraining(t *testing.T) {
	response, err := identitykitex.NewHandler(nil, nil, health.NewRegistry()).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Status != "not_ready" {
		t.Fatalf("Ping() status = %q", response.Status)
	}
}

func TestRegisterMapsUserWithoutPassword(t *testing.T) {
	application := &fakeApplication{user: testUser()}
	response, err := identitykitex.NewHandler(application, nil, nil).Register(context.Background(), &identityrpc.RegisterRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if response.Id != 42 || response.Username != "alice" || response.TokenVersion != 1 {
		t.Fatalf("Register() = %#v", response)
	}
}

func TestRegisterMapsConflictError(t *testing.T) {
	application := &fakeApplication{err: app.ErrUsernameConflict}
	_, err := identitykitex.NewHandler(application, nil, nil).Register(context.Background(), &identityrpc.RegisterRequest{})
	assertRPCStatus(t, err, identityerrors.Conflict, app.ErrUsernameConflict)
}

func TestRegisterReturnsSafeValidationStatus(t *testing.T) {
	cause := &domain.ValidationError{Field: "password", Reason: "private validation detail"}
	application := &fakeApplication{err: cause}
	_, err := identitykitex.NewHandler(application, nil, nil).Register(context.Background(), &identityrpc.RegisterRequest{})
	assertRPCStatus(t, err, identityerrors.InvalidInput, cause)
}

func TestRegisterPreservesInternalCause(t *testing.T) {
	cause := errors.New("private database detail")
	application := &fakeApplication{err: cause}
	_, err := identitykitex.NewHandler(application, nil, nil).Register(context.Background(), &identityrpc.RegisterRequest{})
	assertRPCStatus(t, err, identityerrors.Internal, cause)
}

func TestRegisterRejectsNilRequest(t *testing.T) {
	_, err := identitykitex.NewHandler(&fakeApplication{}, nil, nil).Register(context.Background(), nil)
	assertRPCStatus(t, err, identityerrors.InvalidInput, nil)
}

func TestAuthenticateMapsAccessToken(t *testing.T) {
	now := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	application := &fakeApplication{authentication: &app.Authentication{
		User:        testUser(),
		AccessToken: app.AccessToken{Value: "signed-access-token", ExpiresAt: now.Add(15 * time.Minute)},
	}}
	response, err := identitykitex.NewHandler(application, nil, nil).Authenticate(context.Background(), &identityrpc.AuthenticateRequest{
		Identifier: "alice",
		Password:   "correct-password",
	})
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if response.User.Id != 42 || response.AccessToken != "signed-access-token" || response.ExpiresAtUnix != now.Add(15*time.Minute).Unix() {
		t.Fatalf("Authenticate() = %#v", response)
	}
}

func TestGetUserRequiresMatchingActiveToken(t *testing.T) {
	application := &fakeApplication{user: testUser()}
	verifier := &fakeVerifier{principal: auth.Principal{UserID: 42, Role: domain.RoleUser, TokenVersion: 1}}
	ctx := auth.WithAccessToken(context.Background(), "signed-access-token")

	response, err := identitykitex.NewHandler(application, verifier, nil).GetUser(ctx, &identityrpc.GetUserRequest{UserId: 42})
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if response.Id != 42 || verifier.value != "signed-access-token" || application.getUserCalls != 1 {
		t.Fatalf("GetUser() = %#v, token = %q, calls = %d", response, verifier.value, application.getUserCalls)
	}
}

func TestGetUserRejectsInvalidAccessToken(t *testing.T) {
	application := &fakeApplication{user: testUser()}
	cause := errors.New("invalid token detail")
	verifier := &fakeVerifier{err: cause}

	_, err := identitykitex.NewHandler(application, verifier, nil).GetUser(context.Background(), &identityrpc.GetUserRequest{UserId: 42})
	assertRPCStatus(t, err, identityerrors.Unauthenticated, cause)
	if application.getUserCalls != 0 {
		t.Fatalf("GetUser() application calls = %d, want 0", application.getUserCalls)
	}
}

func TestGetUserRejectsDifferentSubject(t *testing.T) {
	application := &fakeApplication{user: testUser()}
	verifier := &fakeVerifier{principal: auth.Principal{UserID: 7, Role: domain.RoleUser, TokenVersion: 1}}
	ctx := auth.WithAccessToken(context.Background(), "signed-access-token")

	_, err := identitykitex.NewHandler(application, verifier, nil).GetUser(ctx, &identityrpc.GetUserRequest{UserId: 42})
	assertRPCStatus(t, err, identityerrors.Forbidden, nil)
	if application.getUserCalls != 0 {
		t.Fatalf("GetUser() application calls = %d, want 0", application.getUserCalls)
	}
}

func TestGetUserRejectsInactiveOrRevokedToken(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		tokenVersion int64
	}{
		{name: "disabled user", status: domain.StatusDisabled, tokenVersion: 1},
		{name: "stale token version", status: domain.StatusActive, tokenVersion: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user := testUser()
			user.Status = test.status
			user.TokenVersion = test.tokenVersion
			application := &fakeApplication{user: user}
			verifier := &fakeVerifier{principal: auth.Principal{UserID: 42, Role: domain.RoleUser, TokenVersion: 1}}
			ctx := auth.WithAccessToken(context.Background(), "signed-access-token")

			_, err := identitykitex.NewHandler(application, verifier, nil).GetUser(ctx, &identityrpc.GetUserRequest{UserId: 42})
			assertRPCStatus(t, err, identityerrors.Unauthenticated, nil)
		})
	}
}

func assertRPCStatus(t *testing.T, err error, mapping identityerrors.Mapping, cause error) {
	t.Helper()
	bizError, ok := kerrors.FromBizStatusError(err)
	definition := mapping.Definition()
	if !ok || bizError.BizStatusCode() != mapping.Code() || bizError.BizMessage() != definition.SafeMessage() {
		t.Fatalf("business error = %v, want code %d and message %q", err, mapping.Code(), definition.SafeMessage())
	}
	if key, kind := rpcerror.Metadata(err); key != definition.Key() || kind != definition.Kind() {
		t.Fatalf("business metadata = %q %q, want %q %q", key, kind, definition.Key(), definition.Kind())
	}
	if !errors.Is(err, definition) {
		t.Fatalf("business error does not match definition %q", definition.Key())
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("business error does not retain cause %v", cause)
	}
}

type fakeApplication struct {
	user           *domain.User
	authentication *app.Authentication
	err            error
	getUserCalls   int
}

func (f *fakeApplication) Register(context.Context, app.RegisterInput) (*domain.User, error) {
	return f.user, f.err
}

func (f *fakeApplication) Authenticate(context.Context, app.AuthenticateInput) (*app.Authentication, error) {
	return f.authentication, f.err
}

func (f *fakeApplication) GetUser(context.Context, int64) (*domain.User, error) {
	f.getUserCalls++
	return f.user, f.err
}

type fakeVerifier struct {
	principal auth.Principal
	err       error
	value     string
}

func (f *fakeVerifier) Verify(value string) (auth.Principal, error) {
	f.value = value
	return f.principal, f.err
}

func testUser() *domain.User {
	now := time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)
	return &domain.User{
		ID:           42,
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: "must-not-be-mapped",
		Role:         domain.RoleUser,
		Status:       domain.StatusActive,
		TokenVersion: 1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

var _ identitykitex.Application = (*fakeApplication)(nil)

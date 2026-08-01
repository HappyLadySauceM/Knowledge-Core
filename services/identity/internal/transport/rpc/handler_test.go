package rpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identitylogic "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/logic"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type serviceStub struct {
	register func(context.Context, identitylogic.RegisterInput) (*domain.User, error)
}

func (s serviceStub) Register(ctx context.Context, input identitylogic.RegisterInput) (*domain.User, error) {
	return s.register(ctx, input)
}

type readinessStub struct{ err error }

func (s readinessStub) Ready(context.Context) error { return s.err }

type authenticateStub struct {
	authenticate func(context.Context, identitylogic.AuthenticateInput) (*identitylogic.Authentication, error)
}

func (s authenticateStub) Authenticate(ctx context.Context, input identitylogic.AuthenticateInput) (*identitylogic.Authentication, error) {
	return s.authenticate(ctx, input)
}

type getUserStub struct {
	getUser func(context.Context, int64) (*domain.User, error)
}

func (s getUserStub) GetUser(ctx context.Context, id int64) (*domain.User, error) {
	return s.getUser(ctx, id)
}

type verifierStub struct {
	principal coreauth.Principal
	err       error
	value     string
}

func (s *verifierStub) Verify(value string) (coreauth.Principal, error) {
	s.value = value
	return s.principal, s.err
}

func TestHandlerPingReportsReadiness(t *testing.T) {
	handler := newTestHandler(t, serviceStub{register: unexpectedRegister(t)}, readinessStub{})
	handler.now = func() time.Time { return time.Unix(123, 0) }

	response, err := handler.Ping(context.Background(), &commonv1.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != serviceName || response.Status != "ready" || response.UnixTime != 123 {
		t.Fatalf("Ping() = %#v", response)
	}

	handler.readiness = readinessStub{err: errors.New("database unavailable")}
	response, err = handler.Ping(context.Background(), &commonv1.PingRequest{})
	if err != nil || response.Status != "not_ready" {
		t.Fatalf("Ping() = %#v, %v", response, err)
	}
}

func TestHandlerRegisterMapsUserAndErrors(t *testing.T) {
	createdAt := time.Unix(100, 0)
	updatedAt := time.Unix(200, 0)
	handler := newTestHandler(t, serviceStub{register: func(_ context.Context, input identitylogic.RegisterInput) (*domain.User, error) {
		if input.Password != "safe-password" {
			t.Fatalf("Register input = %#v", input)
		}
		return &domain.User{
			ID: 7, Username: input.Username, Email: input.Email,
			Role: domain.RoleUser, Status: domain.StatusActive, TokenVersion: 1,
			CreatedAt: createdAt, UpdatedAt: updatedAt,
		}, nil
	}}, readinessStub{})

	user, err := handler.Register(context.Background(), &identityv1.RegisterRequest{
		Username: "alice", Email: "alice@example.com", Password: "safe-password",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if user.Id != 7 || user.CreatedAtUnix != 100 || user.UpdatedAtUnix != 200 {
		t.Fatalf("Register() = %#v", user)
	}

	_, err = handler.Register(metadata.WithRequestID(context.Background(), "request-123"), nil)
	biz, ok := kerrors.FromBizStatusError(err)
	if !ok || biz.BizStatusCode() != 20001 {
		t.Fatalf("Register(nil) error = %v", err)
	}
	if biz.BizExtra()[apperror.ExtraRequestID] != "request-123" {
		t.Fatalf("Register(nil) extra = %#v", biz.BizExtra())
	}
}

func TestHandlerAuthenticateAndGetUser(t *testing.T) {
	now := time.Unix(500, 0)
	user := &domain.User{
		ID: 42, Username: "alice", Email: "alice@example.com", Role: domain.RoleUser,
		Status: domain.StatusActive, TokenVersion: 3, CreatedAt: now, UpdatedAt: now,
	}
	verifier := &verifierStub{principal: coreauth.Principal{UserID: 42, Role: domain.RoleUser, TokenVersion: 3}}
	handler, err := NewHandler(
		serviceStub{register: unexpectedRegister(t)},
		authenticateStub{authenticate: func(_ context.Context, input identitylogic.AuthenticateInput) (*identitylogic.Authentication, error) {
			if input.Identifier != "alice" || input.Password != "safe-password" {
				t.Fatalf("Authenticate input = %#v", input)
			}
			return &identitylogic.Authentication{
				User:        user,
				AccessToken: coreauth.IssuedToken{Value: "signed-token", ExpiresAt: now.Add(time.Minute)},
			}, nil
		}},
		getUserStub{getUser: func(_ context.Context, id int64) (*domain.User, error) {
			if id != 42 {
				t.Fatalf("GetUser id = %d", id)
			}
			return user, nil
		}},
		verifier,
		readinessStub{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	authentication, err := handler.Authenticate(context.Background(), &identityv1.AuthenticateRequest{
		Identifier: "alice", Password: "safe-password",
	})
	if err != nil || authentication.AccessToken != "signed-token" || authentication.User.Id != 42 {
		t.Fatalf("Authenticate() = %#v, %v", authentication, err)
	}
	ctx := coreauth.WithAccessToken(context.Background(), "signed-token")
	response, err := handler.GetUser(ctx, &identityv1.GetUserRequest{UserId: 42})
	if err != nil || response.Id != 42 || verifier.value != "signed-token" {
		t.Fatalf("GetUser() = %#v, %v, token %q", response, err, verifier.value)
	}
}

func TestHandlerGetUserRejectsStaleOrCrossUserToken(t *testing.T) {
	user := &domain.User{
		ID: 42, Role: domain.RoleUser, Status: domain.StatusActive, TokenVersion: 4,
		CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0),
	}
	tests := []struct {
		name      string
		principal coreauth.Principal
		code      int32
	}{
		{name: "stale token version", principal: coreauth.Principal{UserID: 42, Role: domain.RoleUser, TokenVersion: 3}, code: identityv1.CodeUnauthenticated},
		{name: "cross user", principal: coreauth.Principal{UserID: 7, Role: domain.RoleUser, TokenVersion: 4}, code: identityv1.CodeForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, err := NewHandler(
				serviceStub{register: unexpectedRegister(t)},
				defaultAuthenticateStub(t),
				getUserStub{getUser: func(context.Context, int64) (*domain.User, error) { return user, nil }},
				&verifierStub{principal: test.principal},
				readinessStub{},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = handler.GetUser(
				coreauth.WithAccessToken(context.Background(), "signed-token"),
				&identityv1.GetUserRequest{UserId: 42},
			)
			businessError, ok := kerrors.FromBizStatusError(err)
			if !ok || businessError.BizStatusCode() != test.code {
				t.Fatalf("GetUser() error = %v", err)
			}
		})
	}
}

func TestHandlerRejectsNilServiceResult(t *testing.T) {
	handler := newTestHandler(t, serviceStub{register: func(context.Context, identitylogic.RegisterInput) (*domain.User, error) {
		return nil, nil
	}}, readinessStub{})
	_, err := handler.Register(context.Background(), &identityv1.RegisterRequest{})
	biz, ok := kerrors.FromBizStatusError(err)
	if !ok || biz.BizStatusCode() != identityv1.CodeInternal {
		t.Fatalf("Register() error = %v", err)
	}
}

func TestHandlerLogsInternalCauseWithoutExposingItToClient(t *testing.T) {
	var output bytes.Buffer
	cause := errors.New("database exploded")
	handler, err := NewHandler(
		serviceStub{register: func(context.Context, identitylogic.RegisterInput) (*domain.User, error) { return nil, cause }},
		defaultAuthenticateStub(t), defaultGetUserStub(t), &verifierStub{}, readinessStub{},
		slog.New(slog.NewTextHandler(&output, nil)),
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}

	_, err = handler.Register(context.Background(), &identityv1.RegisterRequest{
		Username: "alice", Email: "alice@example.com", Password: "safe-password",
	})
	biz, ok := kerrors.FromBizStatusError(err)
	if !ok || biz.BizStatusCode() != identityv1.CodeInternal {
		t.Fatalf("Register() error = %v", err)
	}
	if strings.Contains(biz.BizMessage(), cause.Error()) {
		t.Fatalf("client business message leaked internal cause: %q", biz.BizMessage())
	}
	logs := output.String()
	if !strings.Contains(logs, cause.Error()) || !strings.Contains(logs, "identity.internal") {
		t.Fatalf("server log lacks internal diagnostics: %s", logs)
	}
}

func newTestHandler(t *testing.T, service RegisterService, readiness Readiness) *Handler {
	t.Helper()
	handler, err := NewHandler(
		service, defaultAuthenticateStub(t), defaultGetUserStub(t), &verifierStub{}, readiness,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func defaultAuthenticateStub(t *testing.T) authenticateStub {
	t.Helper()
	return authenticateStub{authenticate: func(context.Context, identitylogic.AuthenticateInput) (*identitylogic.Authentication, error) {
		t.Fatal("unexpected Authenticate call")
		return nil, nil
	}}
}

func defaultGetUserStub(t *testing.T) getUserStub {
	t.Helper()
	return getUserStub{getUser: func(context.Context, int64) (*domain.User, error) {
		t.Fatal("unexpected GetUser call")
		return nil, nil
	}}
}

func unexpectedRegister(t *testing.T) func(context.Context, identitylogic.RegisterInput) (*domain.User, error) {
	t.Helper()
	return func(context.Context, identitylogic.RegisterInput) (*domain.User, error) {
		t.Fatal("unexpected Register call")
		return nil, nil
	}
}

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
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	identityapp "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type serviceStub struct {
	register func(context.Context, identityapp.RegisterInput) (*domain.User, error)
}

func (s serviceStub) Register(ctx context.Context, input identityapp.RegisterInput) (*domain.User, error) {
	return s.register(ctx, input)
}

type readinessStub struct{ err error }

func (s readinessStub) Ready(context.Context) error { return s.err }

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
	handler := newTestHandler(t, serviceStub{register: func(_ context.Context, input identityapp.RegisterInput) (*domain.User, error) {
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

func TestHandlerUnimplementedMethodsUseStableCode(t *testing.T) {
	handler := newTestHandler(t, serviceStub{register: unexpectedRegister(t)}, readinessStub{})

	_, authenticateErr := handler.Authenticate(context.Background(), &identityv1.AuthenticateRequest{})
	_, getUserErr := handler.GetUser(context.Background(), &identityv1.GetUserRequest{})
	for method, err := range map[string]error{"Authenticate": authenticateErr, "GetUser": getUserErr} {
		biz, ok := kerrors.FromBizStatusError(err)
		if !ok || biz.BizStatusCode() != identityv1.CodeUnimplemented {
			t.Fatalf("%s() error = %v", method, err)
		}
	}
}

func TestHandlerRejectsNilServiceResult(t *testing.T) {
	handler := newTestHandler(t, serviceStub{register: func(context.Context, identityapp.RegisterInput) (*domain.User, error) {
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
	handler, err := NewHandler(serviceStub{register: func(context.Context, identityapp.RegisterInput) (*domain.User, error) {
		return nil, cause
	}}, readinessStub{}, slog.New(slog.NewTextHandler(&output, nil)))
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

func newTestHandler(t *testing.T, service Service, readiness Readiness) *Handler {
	t.Helper()
	handler, err := NewHandler(service, readiness, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func unexpectedRegister(t *testing.T) func(context.Context, identityapp.RegisterInput) (*domain.User, error) {
	t.Helper()
	return func(context.Context, identityapp.RegisterInput) (*domain.User, error) {
		t.Fatal("unexpected Register call")
		return nil, nil
	}
}

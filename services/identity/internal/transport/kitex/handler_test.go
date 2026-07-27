package kitex_test

import (
	"context"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identitykitex "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestPing(t *testing.T) {
	response, err := identitykitex.NewHandler(nil).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "identity" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

func TestRegisterMapsUserWithoutPassword(t *testing.T) {
	application := &fakeApplication{user: testUser()}
	response, err := identitykitex.NewHandler(application).Register(context.Background(), &identityrpc.RegisterRequest{
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
	_, err := identitykitex.NewHandler(application).Register(context.Background(), &identityrpc.RegisterRequest{})
	bizError, ok := kerrors.FromBizStatusError(err)
	if !ok || bizError.BizStatusCode() != identitykitex.CodeConflict {
		t.Fatalf("Register() error = %v", err)
	}
}

type fakeApplication struct {
	user *domain.User
	err  error
}

func (f *fakeApplication) Register(context.Context, app.RegisterInput) (*domain.User, error) {
	return f.user, f.err
}

func (f *fakeApplication) Authenticate(context.Context, app.AuthenticateInput) (*domain.User, error) {
	return f.user, f.err
}

func (f *fakeApplication) GetUser(context.Context, int64) (*domain.User, error) {
	return f.user, f.err
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

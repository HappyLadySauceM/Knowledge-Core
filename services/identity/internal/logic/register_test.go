package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

type registerUsersStub struct {
	createErr error
}

func (s registerUsersStub) Create(context.Context, *domain.User) error { return s.createErr }
func (s registerUsersStub) FindByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (s registerUsersStub) FindByUsername(context.Context, string) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (s registerUsersStub) FindByLogin(context.Context, string) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (s registerUsersStub) RecordLoginFailure(context.Context, int64, time.Time, time.Time, int) (bool, error) {
	return false, errors.New("unused")
}
func (s registerUsersStub) CompleteLoginSuccess(context.Context, int64, time.Time) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (s registerUsersStub) MarkEmailVerified(context.Context, int64, time.Time) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (s registerUsersStub) UpdatePassword(context.Context, int64, string, time.Time) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (s registerUsersStub) Deactivate(context.Context, int64, time.Time) error {
	return errors.New("unused")
}

type registerVerificationStub struct {
	err error
}

func (s registerVerificationStub) CreateUserAndEnqueue(context.Context, *domain.User, *domain.ActionToken, domain.EmailMessage) error {
	return s.err
}

func TestRegisterVerificationMapsUsernameConflict(t *testing.T) {
	logic, err := NewRegisterLogic(registerUsersStub{}, passwordVerifierStub{}, registerVerificationStub{err: repository.ErrUsernameConflict}, "0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = logic.Register(context.Background(), RegisterInput{Username: "alice", Email: "alice@example.com", Password: "password1"})
	if !errors.Is(err, identityerrors.UsernameConflict) {
		t.Fatalf("Register() error = %v", err)
	}
	if details, ok := apperror.Details(err); !ok || details.Key != "identity.username_conflict" {
		t.Fatalf("Register() details = %#v", details)
	}
}

func TestRegisterVerificationMapsEmailConflict(t *testing.T) {
	logic, err := NewRegisterLogic(registerUsersStub{}, passwordVerifierStub{}, registerVerificationStub{err: repository.ErrEmailConflict}, "0123456789abcdef", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_, err = logic.Register(context.Background(), RegisterInput{Username: "alice", Email: "alice@example.com", Password: "password1"})
	if !errors.Is(err, identityerrors.EmailConflict) {
		t.Fatalf("Register() error = %v", err)
	}
	if details, ok := apperror.Details(err); !ok || details.Key != "identity.email_conflict" {
		t.Fatalf("Register() details = %#v", details)
	}
}

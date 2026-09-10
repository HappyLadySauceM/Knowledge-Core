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

type actionUsersStub struct{}

func (actionUsersStub) Create(context.Context, *domain.User) error { return errors.New("unused") }
func (actionUsersStub) FindByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (actionUsersStub) FindByUsername(context.Context, string) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (actionUsersStub) FindByLogin(context.Context, string) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (actionUsersStub) RecordLoginFailure(context.Context, int64, time.Time, time.Time, int) (bool, error) {
	return false, errors.New("unused")
}
func (actionUsersStub) CompleteLoginSuccess(context.Context, int64, time.Time) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (actionUsersStub) MarkEmailVerified(context.Context, int64, time.Time) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (actionUsersStub) UpdatePassword(context.Context, int64, string, time.Time) (*domain.User, error) {
	return nil, errors.New("unused")
}
func (actionUsersStub) Deactivate(context.Context, int64, time.Time) error {
	return errors.New("unused")
}

type consumeActionsStub struct {
	err error
}

func (s consumeActionsStub) Create(context.Context, *domain.ActionToken) error {
	return errors.New("unused")
}
func (s consumeActionsStub) Consume(context.Context, string, []byte, time.Time) (*domain.ActionToken, error) {
	return nil, errors.New("unused")
}
func (s consumeActionsStub) ConsumeAndVerifyEmail(context.Context, []byte, time.Time) error {
	return s.err
}
func (s consumeActionsStub) ConsumeAndResetPassword(context.Context, []byte, string, time.Time) error {
	return s.err
}

type actionSessionsStub struct{}

func (actionSessionsStub) Create(context.Context, *domain.Session) error { return errors.New("unused") }
func (actionSessionsStub) Find(context.Context, string) (*domain.Session, error) {
	return nil, errors.New("unused")
}
func (actionSessionsStub) Rotate(context.Context, string, []byte, []byte, time.Time, time.Time) (*domain.Session, error) {
	return nil, errors.New("unused")
}
func (actionSessionsStub) Revoke(context.Context, string, string, time.Time) error {
	return errors.New("unused")
}
func (actionSessionsStub) RevokeAll(context.Context, int64, string, time.Time) error {
	return errors.New("unused")
}
func (actionSessionsStub) List(context.Context, int64) ([]*domain.Session, error) {
	return nil, errors.New("unused")
}

func newActionLogic(t *testing.T, consumeErr error) *ActionLogic {
	t.Helper()
	logic, err := NewActionLogic(actionUsersStub{}, consumeActionsStub{err: consumeErr}, actionSessionsStub{}, passwordVerifierStub{}, "0123456789abcdef", time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	return logic
}

func requireActionError(t *testing.T, err error, want error, key string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	details, ok := apperror.Details(err)
	if !ok || details.Key != key {
		t.Fatalf("details = %#v, want key %q", details, key)
	}
}

func TestVerifyEmailRejectsEmptyToken(t *testing.T) {
	err := newActionLogic(t, nil).VerifyEmail(context.Background(), "  ")
	requireActionError(t, err, identityerrors.InvalidInput, "identity.invalid_input")
}

func TestVerifyEmailMapsUnknownTokenToInvalidInput(t *testing.T) {
	err := newActionLogic(t, repository.ErrActionNotFound).VerifyEmail(context.Background(), "ka1.unknown")
	requireActionError(t, err, identityerrors.InvalidInput, "identity.invalid_input")
}

func TestVerifyEmailMapsExpiredToken(t *testing.T) {
	err := newActionLogic(t, repository.ErrActionExpired).VerifyEmail(context.Background(), "ka1.expired")
	requireActionError(t, err, identityerrors.ActionExpired, "identity.action_expired")
}

func TestVerifyEmailMapsAlreadyUsedToken(t *testing.T) {
	err := newActionLogic(t, repository.ErrActionAlreadyUsed).VerifyEmail(context.Background(), "ka1.used")
	requireActionError(t, err, identityerrors.ActionAlreadyUsed, "identity.action_already_used")
}

func TestVerifyEmailSucceedsForValidToken(t *testing.T) {
	if err := newActionLogic(t, nil).VerifyEmail(context.Background(), "ka1.valid"); err != nil {
		t.Fatalf("VerifyEmail() error = %v", err)
	}
}

func TestResetPasswordRejectsEmptyToken(t *testing.T) {
	err := newActionLogic(t, nil).ResetPassword(context.Background(), "  ", "password1")
	requireActionError(t, err, identityerrors.InvalidInput, "identity.invalid_input")
}

func TestResetPasswordMapsUnknownTokenToInvalidInput(t *testing.T) {
	err := newActionLogic(t, repository.ErrActionNotFound).ResetPassword(context.Background(), "ka1.unknown", "password1")
	requireActionError(t, err, identityerrors.InvalidInput, "identity.invalid_input")
}

func TestResetPasswordMapsExpiredToken(t *testing.T) {
	err := newActionLogic(t, repository.ErrActionExpired).ResetPassword(context.Background(), "ka1.expired", "password1")
	requireActionError(t, err, identityerrors.ActionExpired, "identity.action_expired")
}

func TestResetPasswordMapsAlreadyUsedToken(t *testing.T) {
	err := newActionLogic(t, repository.ErrActionAlreadyUsed).ResetPassword(context.Background(), "ka1.used", "password1")
	requireActionError(t, err, identityerrors.ActionAlreadyUsed, "identity.action_already_used")
}

func TestResetPasswordSucceedsForValidToken(t *testing.T) {
	if err := newActionLogic(t, nil).ResetPassword(context.Background(), "ka1.valid", "password1"); err != nil {
		t.Fatalf("ResetPassword() error = %v", err)
	}
}

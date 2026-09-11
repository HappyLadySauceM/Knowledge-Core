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
func (s consumeActionsStub) LatestUnusedByKind(context.Context, int64, string) (*domain.ActionToken, error) {
	return nil, repository.ErrActionNotFound
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

type verificationActionsStub struct {
	unused  *domain.ActionToken
	created int
}

func (s *verificationActionsStub) Create(_ context.Context, token *domain.ActionToken) error {
	s.created++
	s.unused = token
	return nil
}
func (s *verificationActionsStub) Consume(context.Context, string, []byte, time.Time) (*domain.ActionToken, error) {
	return nil, errors.New("unused")
}
func (s *verificationActionsStub) LatestUnusedByKind(_ context.Context, _ int64, _ string) (*domain.ActionToken, error) {
	if s.unused == nil {
		return nil, repository.ErrActionNotFound
	}
	clone := *s.unused
	return &clone, nil
}

func newVerificationLogic(t *testing.T, actions *verificationActionsStub) *ActionLogic {
	t.Helper()
	logic, err := NewActionLogic(actionUsersStub{}, actions, actionSessionsStub{}, passwordVerifierStub{}, "0123456789abcdef", 30*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	logic.now = func() time.Time { return time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC) }
	return logic
}

func pendingVerificationUser() *domain.User {
	return &domain.User{
		ID: 7, Username: "alice", Email: "alice@example.com", Role: domain.RoleUser, Status: domain.StatusPending,
	}
}

func TestEmailVerificationStatusReturnsVerified(t *testing.T) {
	verifiedAt := time.Date(2026, 9, 11, 2, 0, 0, 0, time.UTC)
	user := pendingVerificationUser()
	user.Status = domain.StatusActive
	user.EmailVerifiedAt = &verifiedAt
	status, err := newVerificationLogic(t, &verificationActionsStub{}).EmailVerificationStatus(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != EmailVerificationVerified || status.ExpiresAt != nil || status.RetryAfterSeconds != 0 {
		t.Fatalf("status = %#v", status)
	}
}

func TestEmailVerificationStatusReturnsPendingForUnexpiredToken(t *testing.T) {
	now := time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC)
	expires := now.Add(12 * time.Minute)
	status, err := newVerificationLogic(t, &verificationActionsStub{unused: &domain.ActionToken{
		UserID: 7, Kind: domain.ActionEmailVerification, ExpiresAt: expires,
	}}).EmailVerificationStatus(context.Background(), pendingVerificationUser())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != EmailVerificationPending || status.RetryAfterSeconds != 12*60 || status.ExpiresAt == nil || !status.ExpiresAt.Equal(expires) {
		t.Fatalf("status = %#v", status)
	}
}

func TestEmailVerificationStatusReturnsIdleWhenTokenExpired(t *testing.T) {
	now := time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC)
	status, err := newVerificationLogic(t, &verificationActionsStub{unused: &domain.ActionToken{
		UserID: 7, Kind: domain.ActionEmailVerification, ExpiresAt: now,
	}}).EmailVerificationStatus(context.Background(), pendingVerificationUser())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != EmailVerificationIdle || status.ExpiresAt != nil {
		t.Fatalf("status = %#v", status)
	}
}

func TestRequestEmailVerificationIsIdempotentWhenVerified(t *testing.T) {
	verifiedAt := time.Date(2026, 9, 11, 2, 0, 0, 0, time.UTC)
	user := pendingVerificationUser()
	user.Status = domain.StatusActive
	user.EmailVerifiedAt = &verifiedAt
	actions := &verificationActionsStub{}
	status, err := newVerificationLogic(t, actions).RequestEmailVerification(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != EmailVerificationVerified || actions.created != 0 {
		t.Fatalf("status = %#v, created = %d", status, actions.created)
	}
}

func TestRequestEmailVerificationRejectsUnexpiredToken(t *testing.T) {
	now := time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC)
	actions := &verificationActionsStub{unused: &domain.ActionToken{
		UserID: 7, Kind: domain.ActionEmailVerification, ExpiresAt: now.Add(5 * time.Minute),
	}}
	_, err := newVerificationLogic(t, actions).RequestEmailVerification(context.Background(), pendingVerificationUser())
	requireActionError(t, err, identityerrors.VerificationCooldown, "identity.verification_cooldown")
	if apperror.Extra(err, apperror.ExtraRetryAfter) != "300" {
		t.Fatalf("retry_after = %q", apperror.Extra(err, apperror.ExtraRetryAfter))
	}
	if actions.created != 0 {
		t.Fatalf("created = %d", actions.created)
	}
}

func TestRequestEmailVerificationIssuesAfterExpiry(t *testing.T) {
	now := time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC)
	actions := &verificationActionsStub{unused: &domain.ActionToken{
		UserID: 7, Kind: domain.ActionEmailVerification, ExpiresAt: now,
	}}
	status, err := newVerificationLogic(t, actions).RequestEmailVerification(context.Background(), pendingVerificationUser())
	if err != nil {
		t.Fatal(err)
	}
	if actions.created != 1 || status.State != EmailVerificationPending || status.RetryAfterSeconds != int32((30 * time.Minute).Seconds()) {
		t.Fatalf("status = %#v, created = %d", status, actions.created)
	}
}

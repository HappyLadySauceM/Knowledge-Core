package logic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
)

type sessionUsersStub struct{}

func (sessionUsersStub) FindByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("unused")
}

type sessionRepoStub struct {
	created int
}

func (s *sessionRepoStub) Create(context.Context, *domain.Session) error {
	s.created++
	return nil
}
func (s *sessionRepoStub) Find(context.Context, string) (*domain.Session, error) {
	return nil, errors.New("unused")
}
func (s *sessionRepoStub) Rotate(context.Context, string, []byte, []byte, time.Time, time.Time) (*domain.Session, error) {
	return nil, errors.New("unused")
}
func (s *sessionRepoStub) Revoke(context.Context, string, string, time.Time) error {
	return errors.New("unused")
}
func (s *sessionRepoStub) RevokeAll(context.Context, int64, string, time.Time) error {
	return errors.New("unused")
}
func (s *sessionRepoStub) List(context.Context, int64) ([]*domain.Session, error) {
	return nil, errors.New("unused")
}

func newSessionLogic(t *testing.T, sessions *sessionRepoStub) *SessionLogic {
	t.Helper()
	logic, err := NewSessionLogic(
		sessionUsersStub{},
		sessions,
		tokenIssuerStub{},
		"0123456789abcdef",
		time.Hour,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	return logic
}

func TestSessionCreateAllowsPendingUser(t *testing.T) {
	sessions := &sessionRepoStub{}
	logic := newSessionLogic(t, sessions)
	issued, err := logic.Create(context.Background(), &domain.User{
		ID: 1, Username: "alice", Role: domain.RoleUser, Status: domain.StatusPending, TokenVersion: 1,
	}, "")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if issued.AccessToken.Value == "" || sessions.created != 1 {
		t.Fatalf("issued = %#v, created = %d", issued, sessions.created)
	}
}

func TestSessionCreateRejectsDisabledUser(t *testing.T) {
	sessions := &sessionRepoStub{}
	logic := newSessionLogic(t, sessions)
	_, err := logic.Create(context.Background(), &domain.User{
		ID: 1, Username: "alice", Role: domain.RoleUser, Status: domain.StatusDisabled, TokenVersion: 1,
	}, "")
	if !errors.Is(err, identityerrors.UserDisabled) {
		t.Fatalf("Create() error = %v", err)
	}
	if sessions.created != 0 {
		t.Fatalf("created = %d", sessions.created)
	}
}

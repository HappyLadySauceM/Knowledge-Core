package logic

import (
	"context"
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
)

type getUserRepositoryStub struct {
	user *domain.User
	err  error
}

func (s getUserRepositoryStub) FindByID(context.Context, int64) (*domain.User, error) {
	return s.user, s.err
}

func (s getUserRepositoryStub) FindByUsername(context.Context, string) (*domain.User, error) {
	return s.user, s.err
}

func TestGetUserMapsNotFound(t *testing.T) {
	logic, err := NewGetUserLogic(getUserRepositoryStub{err: repository.ErrUserNotFound})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logic.GetUser(context.Background(), 1); !errors.Is(err, identityerrors.UserNotFound) {
		t.Fatalf("GetUser() error = %v", err)
	}
}

func TestResolveUserMapsNotFound(t *testing.T) {
	logic, err := NewGetUserLogic(getUserRepositoryStub{err: repository.ErrUserNotFound})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logic.ResolveUser(context.Background(), "alice"); !errors.Is(err, identityerrors.UserNotFound) {
		t.Fatalf("ResolveUser() error = %v", err)
	}
}

func TestGetUserRejectsInvalidID(t *testing.T) {
	logic, _ := NewGetUserLogic(getUserRepositoryStub{})
	if _, err := logic.GetUser(context.Background(), 0); !errors.Is(err, identityerrors.InvalidInput) {
		t.Fatalf("GetUser() error = %v", err)
	}
}

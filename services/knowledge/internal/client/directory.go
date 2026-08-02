package client

import (
	"context"
	"errors"
	"fmt"

	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

var (
	ErrDirectoryNotFound     = errors.New("identity directory: user not found")
	ErrDirectoryUnauthorized = errors.New("identity directory: authentication failed")
	ErrDirectoryUnavailable  = errors.New("identity directory: unavailable")
)

type Directory struct {
	client identityservice.Client
}

func NewDirectory(identity identityservice.Client) (*Directory, error) {
	if identity == nil {
		return nil, errors.New("create identity directory: client is required")
	}
	return &Directory{client: identity}, nil
}

func (d *Directory) CurrentUser(ctx context.Context) (domain.PublicUser, error) {
	if d == nil || d.client == nil {
		return domain.PublicUser{}, ErrDirectoryUnavailable
	}
	user, err := d.client.GetCurrentUser(ctx, &identityv1.CurrentUserRequest{})
	if err != nil {
		return domain.PublicUser{}, mapDirectoryError(err)
	}
	if user == nil || user.Id <= 0 || user.Username == "" {
		return domain.PublicUser{}, fmt.Errorf("get current identity user: %w", ErrDirectoryUnavailable)
	}
	return domain.PublicUser{ID: user.Id, Username: user.Username, Avatar: user.Avatar}, nil
}

func (d *Directory) ResolveUser(ctx context.Context, username string) (domain.PublicUser, error) {
	if d == nil || d.client == nil {
		return domain.PublicUser{}, ErrDirectoryUnavailable
	}
	user, err := d.client.ResolveUser(ctx, &identityv1.ResolveUserRequest{Username: username})
	if err != nil {
		return domain.PublicUser{}, mapDirectoryError(err)
	}
	if user == nil || user.Id <= 0 || user.Username == "" {
		return domain.PublicUser{}, fmt.Errorf("resolve identity user: %w", ErrDirectoryUnavailable)
	}
	return domain.PublicUser{ID: user.Id, Username: user.Username, Avatar: user.Avatar}, nil
}

func mapDirectoryError(err error) error {
	if businessError, ok := kerrors.FromBizStatusError(err); ok {
		switch businessError.BizStatusCode() {
		case identityv1.CodeUserNotFound:
			return fmt.Errorf("resolve identity user: %w", ErrDirectoryNotFound)
		case identityv1.CodeUnauthenticated, identityv1.CodeForbidden:
			return fmt.Errorf("call identity directory: %w", ErrDirectoryUnauthorized)
		default:
			return fmt.Errorf("call identity directory: %w", ErrDirectoryUnavailable)
		}
	}
	return fmt.Errorf("call identity directory: %w", ErrDirectoryUnavailable)
}

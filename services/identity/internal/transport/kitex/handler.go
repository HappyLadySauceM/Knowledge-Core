package kitex

import (
	"context"
	"errors"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
)

type Application interface {
	Register(ctx context.Context, input app.RegisterInput) (*domain.User, error)
	Authenticate(ctx context.Context, input app.AuthenticateInput) (*app.Authentication, error)
	GetUser(ctx context.Context, id int64) (*domain.User, error)
}

type TokenVerifier interface {
	Verify(value string) (auth.Principal, error)
}

type Handler struct {
	application Application
	verifier    TokenVerifier
	health      *health.Registry
}

func NewHandler(application Application, verifier TokenVerifier, registry *health.Registry) *Handler {
	return &Handler{application: application, verifier: verifier, health: registry}
}

func (h *Handler) Ping(ctx context.Context, _ *common.PingRequest) (*common.PingResponse, error) {
	status := "not_ready"
	if h != nil && h.health != nil && h.health.Ready(ctx) == nil {
		status = "ok"
	}
	return &common.PingResponse{
		Service:  "identity",
		Status:   status,
		UnixTime: time.Now().UTC().Unix(),
	}, nil
}

func (h *Handler) Register(ctx context.Context, request *identityrpc.RegisterRequest) (*identityrpc.User, error) {
	if request == nil {
		return nil, rpcStatus(identityerrors.InvalidInput, errors.New("identity register request is nil"))
	}
	if h.application == nil {
		return nil, internalError(errors.New("identity application is not configured"))
	}
	user, err := h.application.Register(ctx, app.RegisterInput{
		Username: request.Username,
		Email:    request.Email,
		Password: request.Password,
	})
	if err != nil {
		return nil, mapError(err)
	}
	if user == nil {
		return nil, internalError(errors.New("identity register returned no user"))
	}
	return mapUser(user), nil
}

func (h *Handler) Authenticate(ctx context.Context, request *identityrpc.AuthenticateRequest) (*identityrpc.Authentication, error) {
	if request == nil {
		return nil, rpcStatus(identityerrors.InvalidInput, errors.New("identity authenticate request is nil"))
	}
	if h.application == nil {
		return nil, internalError(errors.New("identity application is not configured"))
	}
	authentication, err := h.application.Authenticate(ctx, app.AuthenticateInput{
		Identifier: request.Identifier,
		Password:   request.Password,
	})
	if err != nil {
		return nil, mapError(err)
	}
	if authentication == nil || authentication.User == nil || authentication.AccessToken.Value == "" || authentication.AccessToken.ExpiresAt.IsZero() {
		return nil, internalError(errors.New("identity authenticate returned an incomplete result"))
	}
	return &identityrpc.Authentication{
		User:          mapUser(authentication.User),
		AccessToken:   authentication.AccessToken.Value,
		ExpiresAtUnix: authentication.AccessToken.ExpiresAt.UTC().Unix(),
	}, nil
}

func (h *Handler) GetUser(ctx context.Context, request *identityrpc.GetUserRequest) (*identityrpc.User, error) {
	if request == nil {
		return nil, rpcStatus(identityerrors.InvalidInput, errors.New("identity get-user request is nil"))
	}
	principal, err := h.authenticate(ctx)
	if err != nil {
		return nil, err
	}
	if principal.UserID != request.UserId {
		return nil, rpcStatus(identityerrors.Forbidden, errors.New("identity token subject does not match requested user"))
	}
	user, err := h.application.GetUser(ctx, request.UserId)
	if err != nil {
		return nil, mapError(err)
	}
	if user == nil {
		return nil, internalError(errors.New("identity get-user returned no user"))
	}
	if user.Status != domain.StatusActive || user.TokenVersion != principal.TokenVersion {
		return nil, rpcStatus(identityerrors.Unauthenticated, errors.New("identity token no longer matches active user state"))
	}
	return mapUser(user), nil
}

func (h *Handler) authenticate(ctx context.Context) (auth.Principal, error) {
	if h.application == nil || h.verifier == nil {
		return auth.Principal{}, internalError(errors.New("identity authorization is not configured"))
	}
	principal, err := h.verifier.Verify(auth.AccessToken(ctx))
	if err != nil {
		return auth.Principal{}, rpcStatus(identityerrors.Unauthenticated, err)
	}
	return principal, nil
}

func mapUser(user *domain.User) *identityrpc.User {
	return &identityrpc.User{
		Id:            user.ID,
		Username:      user.Username,
		Email:         user.Email,
		Role:          user.Role,
		Status:        user.Status,
		TokenVersion:  user.TokenVersion,
		Avatar:        user.Avatar,
		Bio:           user.Bio,
		CreatedAtUnix: user.CreatedAt.UTC().Unix(),
		UpdatedAtUnix: user.UpdatedAt.UTC().Unix(),
	}
}

func mapError(err error) error {
	var validationError *domain.ValidationError
	switch {
	case errors.As(err, &validationError):
		return rpcStatus(identityerrors.InvalidInput, err)
	case errors.Is(err, app.ErrUsernameConflict), errors.Is(err, app.ErrEmailConflict):
		return rpcStatus(identityerrors.Conflict, err)
	case errors.Is(err, app.ErrInvalidCredentials):
		return rpcStatus(identityerrors.InvalidCredentials, err)
	case errors.Is(err, app.ErrAccountLocked):
		return rpcStatus(identityerrors.AccountLocked, err)
	case errors.Is(err, app.ErrUserDisabled):
		return rpcStatus(identityerrors.UserDisabled, err)
	case errors.Is(err, app.ErrUserNotFound):
		return rpcStatus(identityerrors.UserNotFound, err)
	default:
		return internalError(err)
	}
}

func internalError(err error) error {
	return rpcStatus(identityerrors.Internal, err)
}

func rpcStatus(mapping identityerrors.Mapping, cause error) error {
	return rpcerror.New(mapping.Code(), mapping.Definition(), cause)
}

package kitex

import (
	"context"
	"errors"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/klog"
)

const (
	CodeInvalidInput       int32 = 10001
	CodeConflict           int32 = 10002
	CodeInvalidCredentials int32 = 10003
	CodeAccountLocked      int32 = 10004
	CodeUserDisabled       int32 = 10005
	CodeUserNotFound       int32 = 10006
	CodeInternal           int32 = 10999
)

type Application interface {
	Register(ctx context.Context, input app.RegisterInput) (*domain.User, error)
	Authenticate(ctx context.Context, input app.AuthenticateInput) (*domain.User, error)
	GetUser(ctx context.Context, id int64) (*domain.User, error)
}

type Handler struct {
	application Application
}

func NewHandler(application Application) *Handler { return &Handler{application: application} }

func (h *Handler) Ping(context.Context, *common.PingRequest) (*common.PingResponse, error) {
	return &common.PingResponse{
		Service:  "identity",
		Status:   "ok",
		UnixTime: time.Now().UTC().Unix(),
	}, nil
}

func (h *Handler) Register(ctx context.Context, request *identityrpc.RegisterRequest) (*identityrpc.User, error) {
	if request == nil {
		return nil, kerrors.NewBizStatusError(CodeInvalidInput, "invalid register request")
	}
	if h.application == nil {
		return nil, internalError(ctx, errors.New("identity application is not configured"))
	}
	user, err := h.application.Register(ctx, app.RegisterInput{
		Username: request.Username,
		Email:    request.Email,
		Password: request.Password,
	})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	if user == nil {
		return nil, internalError(ctx, errors.New("identity register returned no user"))
	}
	return mapUser(user), nil
}

func (h *Handler) Authenticate(ctx context.Context, request *identityrpc.AuthenticateRequest) (*identityrpc.User, error) {
	if request == nil {
		return nil, kerrors.NewBizStatusError(CodeInvalidInput, "invalid authenticate request")
	}
	if h.application == nil {
		return nil, internalError(ctx, errors.New("identity application is not configured"))
	}
	user, err := h.application.Authenticate(ctx, app.AuthenticateInput{
		Identifier: request.Identifier,
		Password:   request.Password,
	})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	if user == nil {
		return nil, internalError(ctx, errors.New("identity authenticate returned no user"))
	}
	return mapUser(user), nil
}

func (h *Handler) GetUser(ctx context.Context, request *identityrpc.GetUserRequest) (*identityrpc.User, error) {
	if request == nil {
		return nil, kerrors.NewBizStatusError(CodeInvalidInput, "invalid get-user request")
	}
	if h.application == nil {
		return nil, internalError(ctx, errors.New("identity application is not configured"))
	}
	user, err := h.application.GetUser(ctx, request.UserId)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	if user == nil {
		return nil, internalError(ctx, errors.New("identity get-user returned no user"))
	}
	return mapUser(user), nil
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

func mapError(ctx context.Context, err error) error {
	var validationError *domain.ValidationError
	switch {
	case errors.As(err, &validationError):
		return kerrors.NewBizStatusError(CodeInvalidInput, validationError.Error())
	case errors.Is(err, app.ErrUsernameConflict), errors.Is(err, app.ErrEmailConflict):
		return kerrors.NewBizStatusError(CodeConflict, err.Error())
	case errors.Is(err, app.ErrInvalidCredentials):
		return kerrors.NewBizStatusError(CodeInvalidCredentials, "invalid credentials")
	case errors.Is(err, app.ErrAccountLocked):
		return kerrors.NewBizStatusError(CodeAccountLocked, "account is temporarily locked")
	case errors.Is(err, app.ErrUserDisabled):
		return kerrors.NewBizStatusError(CodeUserDisabled, "user is disabled")
	case errors.Is(err, app.ErrUserNotFound):
		return kerrors.NewBizStatusError(CodeUserNotFound, "user not found")
	default:
		return internalError(ctx, err)
	}
}

func internalError(ctx context.Context, err error) error {
	klog.CtxErrorf(ctx, "identity request failed: %v", err)
	return kerrors.NewBizStatusError(CodeInternal, "internal service error")
}

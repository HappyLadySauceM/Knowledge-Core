package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	identitylogic "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/logic"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const serviceName = "identity"

type RegisterService interface {
	Register(context.Context, identitylogic.RegisterInput) (*domain.User, error)
}

type AuthenticateService interface {
	Authenticate(context.Context, identitylogic.AuthenticateInput) (*identitylogic.Authentication, error)
}

type GetUserService interface {
	GetUser(context.Context, int64) (*domain.User, error)
}

type TokenVerifier interface {
	Verify(string) (coreauth.Principal, error)
}

type Readiness interface {
	Ready(context.Context) error
}

type Handler struct {
	register     RegisterService
	authenticate AuthenticateService
	users        GetUserService
	verifier     TokenVerifier
	readiness    Readiness
	logger       *slog.Logger
	now          func() time.Time
}

func NewHandler(
	register RegisterService,
	authenticate AuthenticateService,
	users GetUserService,
	verifier TokenVerifier,
	readiness Readiness,
	logger *slog.Logger,
) (*Handler, error) {
	if register == nil || authenticate == nil || users == nil || verifier == nil || readiness == nil || logger == nil {
		return nil, errors.New("create identity RPC handler: use cases, verifier, readiness, and logger are required")
	}
	return &Handler{
		register: register, authenticate: authenticate, users: users, verifier: verifier,
		readiness: readiness,
		logger:    logger,
		now:       time.Now,
	}, nil
}

func (h *Handler) Ping(ctx context.Context, _ *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	status := "ready"
	if err := h.readiness.Ready(ctx); err != nil {
		status = "not_ready"
	}
	return &commonv1.PingResponse{
		Service:  serviceName,
		Status:   status,
		UnixTime: h.now().UTC().Unix(),
	}, nil
}

func (h *Handler) Register(ctx context.Context, request *identityv1.RegisterRequest) (*identityv1.User, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}

	user, err := h.register.Register(ctx, identitylogic.RegisterInput{
		Username: request.Username,
		Email:    request.Email,
		Password: request.Password,
	})
	if err != nil {
		mapped := mapServiceError(err)
		attributes := []any{
			slog.String("component", "identity.rpc"),
			slog.String("event", "register_failed"),
			slog.String("error_key", apperror.Key(mapped)),
			errorDiagnostic(err),
		}
		recordErrorDiagnostics(ctx, err)
		if apperror.KindOf(mapped) == apperror.KindInternal {
			h.logger.ErrorContext(ctx, "identity registration failed", attributes...)
		} else {
			h.logger.WarnContext(ctx, "identity registration failed", attributes...)
		}
		return nil, apperror.ToKitexBizStatus(ctx, mapped)
	}
	if user == nil {
		mapped := identityerrors.Internal.Wrap(errors.New("identity registration returned a nil user"))
		h.logger.ErrorContext(ctx, "identity registration returned no user",
			slog.String("component", "identity.rpc"),
			slog.String("event", "register_failed"),
			slog.String("error_key", apperror.Key(mapped)),
		)
		return nil, apperror.ToKitexBizStatus(ctx, mapped)
	}

	return toTransportUser(user), nil
}

func errorDiagnostic(err error) slog.Attr {
	attributes := []any{slog.String("type", fmt.Sprintf("%T", err))}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		// PgError.Detail can contain input values. Keep only bounded catalog
		// fields that are useful for operations and safe for centralized logs.
		attributes = append(attributes,
			slog.String("db.system", "postgresql"),
			slog.String("db.sqlstate", postgresError.Code),
			slog.String("db.constraint", postgresError.ConstraintName),
		)
	} else {
		attributes = append(attributes, slog.String("message", err.Error()))
	}
	return slog.Group("error", attributes...)
}

func recordErrorDiagnostics(ctx context.Context, err error) {
	span := oteltrace.SpanFromContext(ctx)
	attributes := []attribute.KeyValue{attribute.String("error.cause.type", fmt.Sprintf("%T", err))}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		attributes = append(attributes,
			attribute.String("db.system", "postgresql"),
			attribute.String("db.response.status_code", postgresError.Code),
			attribute.String("db.constraint.name", postgresError.ConstraintName),
		)
	}
	span.SetAttributes(attributes...)
}

func (h *Handler) Authenticate(ctx context.Context, request *identityv1.AuthenticateRequest) (*identityv1.Authentication, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	authentication, err := h.authenticate.Authenticate(ctx, identitylogic.AuthenticateInput{
		Identifier: request.Identifier,
		Password:   request.Password,
	})
	if err != nil {
		return nil, h.transportError(ctx, "authenticate_failed", err)
	}
	if authentication == nil || authentication.User == nil || authentication.AccessToken.Value == "" || authentication.AccessToken.ExpiresAt.IsZero() {
		return nil, h.transportError(ctx, "authenticate_failed", errors.New("identity authentication returned an incomplete result"))
	}
	return &identityv1.Authentication{
		User:          toTransportUser(authentication.User),
		AccessToken:   authentication.AccessToken.Value,
		ExpiresAtUnix: authentication.AccessToken.ExpiresAt.UTC().Unix(),
	}, nil
}

func (h *Handler) GetUser(ctx context.Context, request *identityv1.GetUserRequest) (*identityv1.User, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.UserId <= 0 {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	principal, err := h.verifier.Verify(coreauth.AccessToken(ctx))
	if err != nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.Unauthenticated.Wrap(err))
	}
	if principal.UserID != request.UserId {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.Forbidden.New())
	}
	user, err := h.users.GetUser(ctx, request.UserId)
	if err != nil {
		if apperror.KindOf(err) == apperror.KindNotFound {
			err = identityerrors.Unauthenticated.Wrap(err)
		}
		return nil, h.transportError(ctx, "get_user_failed", err)
	}
	if user == nil || user.Status != domain.StatusActive || user.TokenVersion != principal.TokenVersion {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.Unauthenticated.New())
	}
	return toTransportUser(user), nil
}

func (h *Handler) transportError(ctx context.Context, event string, err error) error {
	mapped := mapServiceError(err)
	attributes := []any{
		slog.String("component", "identity.rpc"),
		slog.String("event", event),
		slog.String("error_key", apperror.Key(mapped)),
		errorDiagnostic(err),
	}
	recordErrorDiagnostics(ctx, err)
	if apperror.KindOf(mapped) == apperror.KindInternal {
		h.logger.ErrorContext(ctx, "identity RPC operation failed", attributes...)
	} else {
		h.logger.WarnContext(ctx, "identity RPC operation failed", attributes...)
	}
	return apperror.ToKitexBizStatus(ctx, mapped)
}

func mapServiceError(err error) error {
	if _, ok := apperror.Details(err); ok {
		return err
	}
	var validationError *domain.ValidationError
	switch {
	case errors.As(err, &validationError):
		return identityerrors.InvalidInput.Wrap(err)
	default:
		return identityerrors.Internal.Wrap(err)
	}
}

func toTransportUser(user *domain.User) *identityv1.User {
	if user == nil {
		return nil
	}
	return &identityv1.User{
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

var _ identityv1.IdentityService = (*Handler)(nil)

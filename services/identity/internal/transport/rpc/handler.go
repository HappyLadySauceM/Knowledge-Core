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

type SessionService interface {
	Refresh(context.Context, string) (*identitylogic.SessionAuthentication, error)
	List(context.Context, int64) ([]*domain.Session, error)
	Revoke(context.Context, int64, string, string) error
	RevokeAll(context.Context, int64, string) error
}

type ActionService interface {
	RequestEmailVerification(context.Context, string) error
	VerifyEmail(context.Context, string) error
	RequestPasswordReset(context.Context, string) error
	ResetPassword(context.Context, string, string) error
	Deactivate(context.Context, int64, string) error
}

type GetUserService interface {
	GetUser(context.Context, int64) (*domain.User, error)
	ResolveUser(context.Context, string) (*domain.User, error)
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
	sessions     SessionService
	actions      ActionService
	users        GetUserService
	verifier     TokenVerifier
	readiness    Readiness
	logger       *slog.Logger
	now          func() time.Time
}

func NewHandler(
	register RegisterService,
	authenticate AuthenticateService,
	args ...any,
) (*Handler, error) {
	var sessions SessionService
	var actions ActionService
	var users GetUserService
	var verifier TokenVerifier
	var readiness Readiness
	var logger *slog.Logger
	if len(args) == 6 {
		if args[0] != nil {
			var ok bool
			sessions, ok = args[0].(SessionService)
			if !ok {
				return nil, errors.New("create identity RPC handler: invalid session service")
			}
		}
		args = args[1:]
		if args[0] != nil {
			var ok bool
			actions, ok = args[0].(ActionService)
			if !ok {
				return nil, errors.New("create identity RPC handler: invalid action service")
			}
		}
		args = args[1:]
	}
	if len(args) == 4 {
		var ok bool
		users, ok = args[0].(GetUserService)
		if !ok {
			return nil, errors.New("create identity RPC handler: invalid user service")
		}
		verifier, ok = args[1].(TokenVerifier)
		if !ok {
			return nil, errors.New("create identity RPC handler: invalid token verifier")
		}
		readiness, ok = args[2].(Readiness)
		if !ok {
			return nil, errors.New("create identity RPC handler: invalid readiness service")
		}
		logger, ok = args[3].(*slog.Logger)
		if !ok {
			return nil, errors.New("create identity RPC handler: invalid logger")
		}
	}
	if register == nil || authenticate == nil || users == nil || verifier == nil || readiness == nil || logger == nil {
		return nil, errors.New("create identity RPC handler: use cases, verifier, readiness, and logger are required")
	}
	return &Handler{
		register: register, authenticate: authenticate, sessions: sessions, actions: actions, users: users, verifier: verifier,
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
		User:         toTransportUser(authentication.User),
		AccessToken:  authentication.AccessToken.Value,
		ExpiresAt:    authentication.AccessToken.ExpiresAt.UTC().Format(time.RFC3339Nano),
		RefreshToken: optionalString(authentication.RefreshToken),
		SessionId:    optionalString(authentication.SessionID),
		TokenType:    optionalString("Bearer"),
	}, nil
}

func (h *Handler) RefreshSession(ctx context.Context, request *identityv1.RefreshSessionRequest) (*identityv1.Authentication, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.RefreshToken == "" || h.sessions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	authentication, err := h.sessions.Refresh(ctx, request.RefreshToken)
	if err != nil {
		return nil, h.transportError(ctx, "refresh_session_failed", err)
	}
	if authentication == nil || authentication.User == nil || authentication.AccessToken.Value == "" || authentication.AccessToken.ExpiresAt.IsZero() || authentication.RefreshToken == "" || authentication.SessionID == "" {
		return nil, h.transportError(ctx, "refresh_session_failed", errors.New("identity refresh returned an incomplete result"))
	}
	return &identityv1.Authentication{
		User:         toTransportUser(authentication.User),
		AccessToken:  authentication.AccessToken.Value,
		ExpiresAt:    authentication.AccessToken.ExpiresAt.UTC().Format(time.RFC3339Nano),
		RefreshToken: optionalString(authentication.RefreshToken),
		SessionId:    optionalString(authentication.SessionID),
		TokenType:    optionalString("Bearer"),
	}, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func emptyResponse() *commonv1.EmptyResponse { return &commonv1.EmptyResponse{} }

func (h *Handler) RequestEmailVerification(ctx context.Context, request *identityv1.EmailRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.Email == "" || h.actions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	if err := h.actions.RequestEmailVerification(ctx, request.Email); err != nil {
		return nil, h.transportError(ctx, "request_email_verification_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) VerifyEmail(ctx context.Context, request *identityv1.EmailTokenRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.Token == "" || h.actions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	if err := h.actions.VerifyEmail(ctx, request.Token); err != nil {
		return nil, h.transportError(ctx, "verify_email_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) RequestPasswordReset(ctx context.Context, request *identityv1.PasswordResetRequestRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.Identifier == "" || h.actions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	if err := h.actions.RequestPasswordReset(ctx, request.Identifier); err != nil {
		return nil, h.transportError(ctx, "request_password_reset_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) ResetPassword(ctx context.Context, request *identityv1.PasswordResetRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.Token == "" || request.Password == "" || h.actions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	if err := h.actions.ResetPassword(ctx, request.Token, request.Password); err != nil {
		return nil, h.transportError(ctx, "reset_password_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) ListSessions(ctx context.Context, request *identityv1.CurrentUserRequest) (*identityv1.SessionList, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || h.sessions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	principal, _, err := h.authenticateRequest(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := h.sessions.List(ctx, principal.UserID)
	if err != nil {
		return nil, h.transportError(ctx, "list_sessions_failed", err)
	}
	result := &identityv1.SessionList{Items: make([]*identityv1.Session, 0, len(sessions))}
	for _, session := range sessions {
		if session == nil {
			continue
		}
		result.Items = append(result.Items, &identityv1.Session{Id: session.ID, DeviceLabel: session.DeviceLabel, CreatedAt: session.CreatedAt.UTC().Format(time.RFC3339Nano), LastSeenAt: session.LastSeenAt.UTC().Format(time.RFC3339Nano), ExpiresAt: session.ExpiresAt.UTC().Format(time.RFC3339Nano), Current: session.ID == principal.SessionID})
	}
	return result, nil
}

func (h *Handler) RevokeSession(ctx context.Context, request *identityv1.SessionRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.SessionId == "" || h.sessions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	principal, _, err := h.authenticateRequest(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.sessions.Revoke(ctx, principal.UserID, request.SessionId, "user_revoked"); err != nil {
		return nil, h.transportError(ctx, "revoke_session_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) RevokeAllSessions(ctx context.Context, request *identityv1.CurrentUserRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || h.sessions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	principal, _, err := h.authenticateRequest(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.sessions.RevokeAll(ctx, principal.UserID, "user_revoked_all"); err != nil {
		return nil, h.transportError(ctx, "revoke_all_sessions_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) DeactivateAccount(ctx context.Context, request *identityv1.DeactivateAccountRequest) (*commonv1.EmptyResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.Password == "" || h.actions == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	principal, _, err := h.authenticateRequest(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.actions.Deactivate(ctx, principal.UserID, request.Password); err != nil {
		return nil, h.transportError(ctx, "deactivate_account_failed", err)
	}
	return emptyResponse(), nil
}

func (h *Handler) GetCurrentUser(ctx context.Context, request *identityv1.CurrentUserRequest) (*identityv1.User, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	principal, user, err := h.authenticateRequest(ctx)
	if err != nil {
		return nil, err
	}
	if user.ID != principal.UserID {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.Unauthenticated.New())
	}
	return toTransportUser(user), nil
}

func (h *Handler) ResolveUser(ctx context.Context, request *identityv1.ResolveUserRequest) (*identityv1.PublicUser, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.InvalidInput.New())
	}
	if _, _, err := h.authenticateRequest(ctx); err != nil {
		return nil, err
	}
	user, err := h.users.ResolveUser(ctx, request.Username)
	if err != nil {
		return nil, h.transportError(ctx, "resolve_user_failed", err)
	}
	if user == nil || user.Status != domain.StatusActive {
		return nil, apperror.ToKitexBizStatus(ctx, identityerrors.UserNotFound.New())
	}
	return &identityv1.PublicUser{Id: user.ID, Username: user.Username, Avatar: user.Avatar}, nil
}

func (h *Handler) authenticateRequest(ctx context.Context) (coreauth.Principal, *domain.User, error) {
	principal, err := h.verifier.Verify(coreauth.AccessToken(ctx))
	if err != nil {
		return coreauth.Principal{}, nil, apperror.ToKitexBizStatus(ctx, identityerrors.Unauthenticated.Wrap(err))
	}
	user, err := h.users.GetUser(ctx, principal.UserID)
	if err != nil {
		if apperror.KindOf(err) == apperror.KindNotFound {
			err = identityerrors.Unauthenticated.Wrap(err)
		}
		return coreauth.Principal{}, nil, h.transportError(ctx, "get_current_user_failed", err)
	}
	if user == nil || user.Status != domain.StatusActive || user.TokenVersion != principal.TokenVersion {
		return coreauth.Principal{}, nil, apperror.ToKitexBizStatus(ctx, identityerrors.Unauthenticated.New())
	}
	return principal, user, nil
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
		Id:           user.ID,
		Username:     user.Username,
		Email:        user.Email,
		Role:         user.Role,
		Status:       user.Status,
		TokenVersion: user.TokenVersion,
		Avatar:       user.Avatar,
		Bio:          user.Bio,
		CreatedAt:    user.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:    user.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

var _ identityv1.IdentityService = (*Handler)(nil)

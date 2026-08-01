package middleware

import (
	"context"
	"errors"
	"net/http"

	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type ResponseError struct {
	Code       int32
	HTTPStatus int
	Definition apperror.Definition
}

var (
	ErrNotReady                = responseError(gatewaymodel.CodeNotReady, "gateway.not_ready", apperror.KindUnavailable, "service unavailable")
	ErrInvalidRequest          = responseError(gatewaymodel.CodeInvalidRequest, "gateway.invalid_request", apperror.KindInvalidArgument, "invalid request")
	ErrAuthenticationRequired  = responseError(gatewaymodel.CodeAuthenticationRequired, "gateway.authentication_required", apperror.KindUnauthenticated, "authentication required")
	ErrPermissionDenied        = responseError(gatewaymodel.CodePermissionDenied, "gateway.permission_denied", apperror.KindPermissionDenied, "permission denied")
	ErrDependencyUnavailable   = responseError(gatewaymodel.CodeDependencyUnavailable, "gateway.dependency_unavailable", apperror.KindUnavailable, "service unavailable")
	ErrRouteNotFound           = responseError(gatewaymodel.CodeRouteNotFound, "gateway.route_not_found", apperror.KindNotFound, "route not found")
	ErrMethodNotAllowed        = responseErrorWithStatus(gatewaymodel.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "gateway.method_not_allowed", apperror.KindInvalidArgument, "method not allowed")
	ErrRateLimited             = responseError(gatewaymodel.CodeRateLimited, "gateway.rate_limited", apperror.KindRateLimited, "rate limit exceeded")
	ErrUpstreamTimeout         = responseError(gatewaymodel.CodeUpstreamTimeout, "gateway.upstream_timeout", apperror.KindDeadlineExceeded, "upstream request timed out")
	ErrInvalidUpstreamResponse = responseErrorWithStatus(gatewaymodel.CodeInvalidUpstreamResponse, http.StatusBadGateway, "gateway.invalid_upstream_response", apperror.KindUnavailable, "invalid upstream response")
	ErrUnimplemented           = responseErrorWithStatus(gatewaymodel.CodeUnimplemented, http.StatusNotImplemented, "gateway.unimplemented", apperror.KindUnimplemented, "operation is not implemented")
	ErrInternal                = responseError(gatewaymodel.CodeInternal, "gateway.internal", apperror.KindInternal, "internal server error")
)

var identityErrors = []rpcErrorMapping{
	{identityv1.CodeInvalidInput, "identity.invalid_input", responseError(identityv1.CodeInvalidInput, "identity.invalid_input", apperror.KindInvalidArgument, "invalid identity input")},
	{identityv1.CodeConflict, "identity.username_conflict", responseError(identityv1.CodeConflict, "identity.username_conflict", apperror.KindConflict, "username already exists")},
	{identityv1.CodeConflict, "identity.email_conflict", responseError(identityv1.CodeConflict, "identity.email_conflict", apperror.KindConflict, "email already exists")},
	{identityv1.CodeInvalidCredentials, "identity.invalid_credentials", responseError(identityv1.CodeInvalidCredentials, "identity.invalid_credentials", apperror.KindUnauthenticated, "invalid credentials")},
	{identityv1.CodeAccountLocked, "identity.account_locked", responseErrorWithStatus(identityv1.CodeAccountLocked, http.StatusLocked, "identity.account_locked", apperror.KindPermissionDenied, "account is locked")},
	{identityv1.CodeUserDisabled, "identity.user_disabled", responseError(identityv1.CodeUserDisabled, "identity.user_disabled", apperror.KindPermissionDenied, "user is disabled")},
	{identityv1.CodeUserNotFound, "identity.user_not_found", responseError(identityv1.CodeUserNotFound, "identity.user_not_found", apperror.KindNotFound, "user not found")},
	{identityv1.CodeUnauthenticated, "identity.unauthenticated", responseError(identityv1.CodeUnauthenticated, "identity.unauthenticated", apperror.KindUnauthenticated, "authentication is required")},
	{identityv1.CodeForbidden, "identity.forbidden", responseError(identityv1.CodeForbidden, "identity.forbidden", apperror.KindPermissionDenied, "access is forbidden")},
	{identityv1.CodeUnimplemented, "identity.unimplemented", responseErrorWithStatus(identityv1.CodeUnimplemented, http.StatusNotImplemented, "identity.unimplemented", apperror.KindUnimplemented, "identity operation is not implemented")},
	{identityv1.CodeInternal, "identity.internal", responseErrorWithStatus(identityv1.CodeInternal, http.StatusBadGateway, "identity.internal", apperror.KindUnavailable, "identity service unavailable")},
}

type rpcErrorMapping struct {
	code     int32
	key      string
	response ResponseError
}

func WriteError(ctx context.Context, request *app.RequestContext, responseError ResponseError) {
	requestID, traceID := responseMetadata(ctx, request)
	payload := &gatewaymodel.ErrorResponse{
		Code: responseError.Code, Message: responseError.Definition.Message,
		Data: &gatewaymodel.EmptyData{}, RequestID: requestID, TraceID: traceID,
	}
	request.Abort()
	WriteJSON(request, responseError.HTTPStatus, payload)
}

func WriteIdentityError(ctx context.Context, request *app.RequestContext, err error) {
	if businessError, ok := kerrors.FromBizStatusError(err); ok {
		key := businessError.BizExtra()[apperror.ExtraErrorKey]
		for _, mapping := range identityErrors {
			if mapping.code == businessError.BizStatusCode() && mapping.key == key {
				WriteError(ctx, request, mapping.response)
				return
			}
		}
		WriteError(ctx, request, ErrInvalidUpstreamResponse)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || kerrors.IsTimeoutError(err) {
		WriteError(ctx, request, ErrUpstreamTimeout)
		return
	}
	WriteError(ctx, request, ErrDependencyUnavailable)
}

func WriteJSON(request *app.RequestContext, status int, value any) {
	payload, err := jsoncodec.Marshal(value)
	if err != nil {
		request.Data(consts.StatusInternalServerError, consts.MIMEApplicationJSONUTF8, []byte(`{"code":10999,"message":"internal server error","data":{},"request_id":""}`))
		return
	}
	request.Data(status, consts.MIMEApplicationJSONUTF8, payload)
}

func ResponseMetadata(ctx context.Context, request *app.RequestContext) (string, *string) {
	return responseMetadata(ctx, request)
}

func responseMetadata(ctx context.Context, request *app.RequestContext) (string, *string) {
	ctx = metadata.EnsureRequestID(ctx)
	requestID := metadata.RequestID(ctx)
	traceIDValue := coretrace.TraceID(ctx)
	request.Header(coretrace.RequestIDHeader, requestID)
	if traceIDValue == "" {
		return requestID, nil
	}
	request.Header(coretrace.TraceIDHeader, traceIDValue)
	return requestID, &traceIDValue
}

func responseError(code int32, key string, kind apperror.Kind, message string) ResponseError {
	definition := apperror.MustDefine(code, key, kind, message)
	status, _ := apperror.ToHTTPError(context.Background(), definition.New())
	return ResponseError{Code: code, HTTPStatus: status, Definition: definition}
}

func responseErrorWithStatus(code int32, status int, key string, kind apperror.Kind, message string) ResponseError {
	return ResponseError{Code: code, HTTPStatus: status, Definition: apperror.MustDefine(code, key, kind, message)}
}

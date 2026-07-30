package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type ResponseError struct {
	code       int32
	status     int
	definition apperror.Definition
}

func (e ResponseError) Code() int32                     { return e.code }
func (e ResponseError) Status() int                     { return e.status }
func (e ResponseError) Definition() apperror.Definition { return e.definition }

var (
	ErrNotReady = responseError(10001,
		apperror.MustDefine("gateway.not_ready", apperror.KindUnavailable, "service unavailable"))
	ErrInvalidRequest = responseError(10002,
		apperror.MustDefine("gateway.invalid_request", apperror.KindInvalidArgument, "invalid request"))
	ErrAuthenticationRequired = responseError(10003,
		apperror.MustDefine("gateway.authentication_required", apperror.KindUnauthenticated, "authentication required"))
	ErrPermissionDenied = responseError(10005,
		apperror.MustDefine("gateway.permission_denied", apperror.KindPermissionDenied, "permission denied"))
	ErrDependencyUnavailable = responseError(10007,
		apperror.MustDefine("gateway.dependency_unavailable", apperror.KindUnavailable, "service unavailable"))
	ErrRouteNotFound = responseError(10008,
		apperror.MustDefine("gateway.route_not_found", apperror.KindNotFound, "route not found"))
	ErrMethodNotAllowed = responseErrorWithStatus(10009, http.StatusMethodNotAllowed,
		apperror.MustDefine("gateway.method_not_allowed", apperror.KindInvalidArgument, "method not allowed"))
	ErrRateLimited = responseError(10010,
		apperror.MustDefine("gateway.rate_limited", apperror.KindRateLimited, "rate limit exceeded"))
	ErrUpstreamTimeout = responseError(10011,
		apperror.MustDefine("gateway.upstream_timeout", apperror.KindDeadlineExceeded, "upstream request timed out"))
	ErrInvalidUpstreamResponse = responseErrorWithStatus(10012, http.StatusBadGateway,
		apperror.MustDefine("gateway.invalid_upstream_response", apperror.KindUnavailable, "invalid upstream response"))
	ErrInternal = responseError(10999,
		apperror.MustDefine("gateway.internal", apperror.KindInternal, "internal server error"))
)

var identityErrors = []rpcErrorMapping{
	{identityrpc.CodeInvalidInput, responseError(identityrpc.CodeInvalidInput,
		apperror.MustDefine("identity.invalid_input", apperror.KindInvalidArgument, "invalid request"))},
	{identityrpc.CodeConflict, responseError(identityrpc.CodeConflict,
		apperror.MustDefine("identity.conflict", apperror.KindConflict, "account already exists"))},
	{identityrpc.CodeInvalidCredentials, responseError(identityrpc.CodeInvalidCredentials,
		apperror.MustDefine("identity.invalid_credentials", apperror.KindUnauthenticated, "invalid credentials"))},
	{identityrpc.CodeAccountLocked, responseErrorWithStatus(identityrpc.CodeAccountLocked, http.StatusLocked,
		apperror.MustDefine("identity.account_locked", apperror.KindPermissionDenied, "account is temporarily locked"))},
	{identityrpc.CodeUserDisabled, responseError(identityrpc.CodeUserDisabled,
		apperror.MustDefine("identity.user_disabled", apperror.KindPermissionDenied, "account is disabled"))},
	{identityrpc.CodeUserNotFound, responseError(identityrpc.CodeUserNotFound,
		apperror.MustDefine("identity.user_not_found", apperror.KindNotFound, "user not found"))},
	{identityrpc.CodeUnauthenticated, responseError(identityrpc.CodeUnauthenticated,
		apperror.MustDefine("identity.unauthenticated", apperror.KindUnauthenticated, "authentication required"))},
	{identityrpc.CodeForbidden, responseError(identityrpc.CodeForbidden,
		apperror.MustDefine("identity.forbidden", apperror.KindPermissionDenied, "permission denied"))},
	{identityrpc.CodeInternal, responseErrorWithStatus(identityrpc.CodeInternal, http.StatusBadGateway,
		apperror.MustDefine("identity.internal", apperror.KindUnavailable, "service unavailable"))},
}

var knowledgeErrors = []rpcErrorMapping{
	{knowledgerpc.CodeInvalidInput, responseError(knowledgerpc.CodeInvalidInput,
		apperror.MustDefine("knowledge.invalid_input", apperror.KindInvalidArgument, "invalid request"))},
	{knowledgerpc.CodeNotFound, responseError(knowledgerpc.CodeNotFound,
		apperror.MustDefine("knowledge.not_found", apperror.KindNotFound, "document not found"))},
	{knowledgerpc.CodeConflict, responseError(knowledgerpc.CodeConflict,
		apperror.MustDefine("knowledge.conflict", apperror.KindConflict, "document version conflict"))},
	{knowledgerpc.CodeForbidden, responseError(knowledgerpc.CodeForbidden,
		apperror.MustDefine("knowledge.forbidden", apperror.KindPermissionDenied, "permission denied"))},
	{knowledgerpc.CodeInternal, responseErrorWithStatus(knowledgerpc.CodeInternal, http.StatusBadGateway,
		apperror.MustDefine("knowledge.internal", apperror.KindUnavailable, "service unavailable"))},
}

type rpcErrorMapping struct {
	code     int32
	response ResponseError
}

type errorEnvelope struct {
	Code      int32    `json:"code"`
	Message   string   `json:"message"`
	Data      struct{} `json:"data"`
	RequestID string   `json:"request_id"`
	TraceID   string   `json:"trace_id"`
}

func HTTPStatus(kind apperror.Kind) int {
	switch kind {
	case apperror.KindInvalidArgument:
		return http.StatusBadRequest
	case apperror.KindUnauthenticated:
		return http.StatusUnauthorized
	case apperror.KindPermissionDenied:
		return http.StatusForbidden
	case apperror.KindNotFound:
		return http.StatusNotFound
	case apperror.KindConflict:
		return http.StatusConflict
	case apperror.KindRateLimited:
		return http.StatusTooManyRequests
	case apperror.KindDeadlineExceeded:
		return http.StatusGatewayTimeout
	case apperror.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func WriteError(request *app.RequestContext, responseError ResponseError) {
	writeApplicationError(request, responseError, responseError.definition.New())
}

func WriteIdentityError(_ context.Context, request *app.RequestContext, err error) {
	writeRPCError(request, err, identityErrors)
}

func WriteKnowledgeError(_ context.Context, request *app.RequestContext, err error) {
	writeRPCError(request, err, knowledgeErrors)
}

func writeRPCError(request *app.RequestContext, err error, mappings []rpcErrorMapping) {
	if bizError, ok := kerrors.FromBizStatusError(err); ok {
		for _, mapping := range mappings {
			if mapping.code == bizError.BizStatusCode() {
				writeApplicationError(request, mapping.response, mapping.response.definition.Wrap(err))
				return
			}
		}
		writeApplicationError(request, ErrInvalidUpstreamResponse, ErrInvalidUpstreamResponse.definition.Wrap(err))
		return
	}

	if errors.Is(err, context.DeadlineExceeded) || kerrors.IsTimeoutError(err) {
		writeApplicationError(request, ErrUpstreamTimeout, ErrUpstreamTimeout.definition.Wrap(err))
		return
	}
	writeApplicationError(request, ErrDependencyUnavailable, ErrDependencyUnavailable.definition.Wrap(err))
}

func writeApplicationError(request *app.RequestContext, responseError ResponseError, err error) {
	requestID, traceID := ensureResponseMetadata(request)
	request.Abort()
	request.JSON(responseError.status, &errorEnvelope{
		Code: responseError.code, Message: apperror.SafeMessage(err), RequestID: requestID, TraceID: traceID,
	})
}

func responseError(code int32, definition apperror.Definition) ResponseError {
	return responseErrorWithStatus(code, HTTPStatus(definition.Kind()), definition)
}

func responseErrorWithStatus(code int32, status int, definition apperror.Definition) ResponseError {
	return ResponseError{code: code, status: status, definition: definition}
}

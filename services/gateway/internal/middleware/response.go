package middleware

import (
	"context"
	"errors"
	"net/http"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
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
	ErrPreconditionFailed      = responseErrorWithStatus(gatewaymodel.CodePreconditionFailed, http.StatusPreconditionFailed, "gateway.precondition_failed", apperror.KindConflict, "resource revision does not match")
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
	{identityv1.CodeEmailNotVerified, "identity.email_not_verified", responseError(identityv1.CodeEmailNotVerified, "identity.email_not_verified", apperror.KindPermissionDenied, "email verification is required")},
	{identityv1.CodeInternal, "identity.internal", responseErrorWithStatus(identityv1.CodeInternal, http.StatusBadGateway, "identity.internal", apperror.KindUnavailable, "identity service unavailable")},
}

var knowledgeErrors = []rpcErrorMapping{
	{knowledgev1.CodeInvalidInput, "knowledge.invalid_input", responseError(knowledgev1.CodeInvalidInput, "knowledge.invalid_input", apperror.KindInvalidArgument, "invalid knowledge input")},
	{knowledgev1.CodeNotFound, "knowledge.not_found", responseError(knowledgev1.CodeNotFound, "knowledge.not_found", apperror.KindNotFound, "resource not found")},
	{knowledgev1.CodeConflict, "knowledge.conflict", responseError(knowledgev1.CodeConflict, "knowledge.conflict", apperror.KindConflict, "resource conflict")},
	{knowledgev1.CodeForbidden, "knowledge.forbidden", responseError(knowledgev1.CodeForbidden, "knowledge.forbidden", apperror.KindPermissionDenied, "permission denied")},
	{knowledgev1.CodeUnauthenticated, "knowledge.unauthenticated", responseError(knowledgev1.CodeUnauthenticated, "knowledge.unauthenticated", apperror.KindUnauthenticated, "authentication required")},
	{knowledgev1.CodeUnavailable, "knowledge.unavailable", responseError(knowledgev1.CodeUnavailable, "knowledge.unavailable", apperror.KindUnavailable, "service unavailable")},
	{knowledgev1.CodePreconditionFailed, "knowledge.precondition_failed", responseErrorWithStatus(knowledgev1.CodePreconditionFailed, http.StatusPreconditionFailed, "knowledge.precondition_failed", apperror.KindConflict, "resource revision does not match")},
	{knowledgev1.CodeGone, "knowledge.gone", responseErrorWithStatus(knowledgev1.CodeGone, http.StatusGone, "knowledge.gone", apperror.KindNotFound, "resource is permanently unavailable")},
	{knowledgev1.CodeQuotaExceeded, "knowledge.quota_exceeded", responseError(knowledgev1.CodeQuotaExceeded, "knowledge.quota_exceeded", apperror.KindConflict, "storage quota exceeded")},
	{knowledgev1.CodeInternal, "knowledge.internal", responseErrorWithStatus(knowledgev1.CodeInternal, http.StatusBadGateway, "knowledge.internal", apperror.KindUnavailable, "knowledge service unavailable")},
}

var collaborationErrors = []rpcErrorMapping{
	{collaborationv1.CodeInvalidInput, "collaboration.invalid_input", responseErrorWithStatus(collaborationv1.CodeInvalidInput, http.StatusBadRequest, "collaboration.invalid_input", apperror.KindInvalidArgument, "invalid collaboration input")},
	{collaborationv1.CodeUnauthenticated, "collaboration.unauthenticated", responseErrorWithStatus(collaborationv1.CodeUnauthenticated, http.StatusUnauthorized, "collaboration.unauthenticated", apperror.KindUnauthenticated, "authentication required")},
	{collaborationv1.CodeForbidden, "collaboration.forbidden", responseErrorWithStatus(collaborationv1.CodeForbidden, http.StatusForbidden, "collaboration.forbidden", apperror.KindPermissionDenied, "permission denied")},
	{collaborationv1.CodeNotFound, "collaboration.not_found", responseErrorWithStatus(collaborationv1.CodeNotFound, http.StatusNotFound, "collaboration.not_found", apperror.KindNotFound, "version not found")},
	{collaborationv1.CodeConflict, "collaboration.conflict", responseErrorWithStatus(collaborationv1.CodeConflict, http.StatusConflict, "collaboration.conflict", apperror.KindConflict, "resource conflict")},
	{collaborationv1.CodePreconditionFailed, "collaboration.precondition_failed", responseErrorWithStatus(collaborationv1.CodePreconditionFailed, http.StatusPreconditionFailed, "collaboration.precondition_failed", apperror.KindConflict, "document sequence does not match")},
	{collaborationv1.CodeUnavailable, "collaboration.unavailable", responseErrorWithStatus(collaborationv1.CodeUnavailable, http.StatusServiceUnavailable, "collaboration.unavailable", apperror.KindUnavailable, "service unavailable")},
	{collaborationv1.CodeUnavailable, "collaboration.deadline_exceeded", ErrUpstreamTimeout},
	{collaborationv1.CodeInternal, "collaboration.internal", responseErrorWithStatus(collaborationv1.CodeInternal, http.StatusBadGateway, "collaboration.internal", apperror.KindUnavailable, "collaboration service unavailable")},
}

type rpcErrorMapping struct {
	code     int32
	key      string
	response ResponseError
}

func WriteError(ctx context.Context, request *app.RequestContext, responseError ResponseError) {
	requestID, traceID := responseMetadata(ctx, request)
	problemContext := metadata.WithRequestID(ctx, requestID)
	payload := apperror.ToHTTPProblem(problemContext, responseError.HTTPStatus, responseError.Definition.New())
	if traceID != nil {
		payload.TraceID = *traceID
	}
	request.Abort()
	writeProblem(request, responseError.HTTPStatus, payload)
}

func WriteIdentityError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err, identityErrors)
}

func WriteKnowledgeError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err, knowledgeErrors)
}

func WriteCollaborationError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err, collaborationErrors)
}

func writeRPCError(ctx context.Context, request *app.RequestContext, err error, mappings []rpcErrorMapping) {
	if businessError, ok := kerrors.FromBizStatusError(err); ok {
		key := businessError.BizExtra()[apperror.ExtraErrorKey]
		for _, mapping := range mappings {
			if mapping.code == businessError.BizStatusCode() && mapping.key == key {
				WriteError(ctx, request, mapping.response)
				return
			}
		}
		WriteError(ctx, request, ErrInvalidUpstreamResponse)
		return
	}
	if isTimeout(err) {
		WriteError(ctx, request, ErrUpstreamTimeout)
		return
	}
	WriteError(ctx, request, ErrDependencyUnavailable)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || kerrors.IsTimeoutError(err) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func WriteJSON(request *app.RequestContext, status int, value any) {
	payload, err := jsoncodec.Marshal(value)
	if err != nil {
		request.Data(
			consts.StatusInternalServerError,
			apperror.ProblemContentType,
			[]byte(`{"type":"urn:knowledge-core:problem:common.internal","title":"Internal Server Error","status":500,"detail":"internal server error","code":1,"key":"common.internal"}`),
		)
		return
	}
	request.Data(status, consts.MIMEApplicationJSONUTF8, payload)
}

func writeProblem(request *app.RequestContext, status int, value any) {
	payload, err := jsoncodec.Marshal(value)
	if err != nil {
		request.Data(
			consts.StatusInternalServerError,
			apperror.ProblemContentType,
			[]byte(`{"type":"urn:knowledge-core:problem:common.internal","title":"Internal Server Error","status":500,"detail":"internal server error","code":1,"key":"common.internal"}`),
		)
		return
	}
	request.Data(status, apperror.ProblemContentType, payload)
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

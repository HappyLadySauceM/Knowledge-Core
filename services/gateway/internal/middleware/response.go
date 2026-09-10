package middleware

import (
	"context"
	"errors"
	"net/http"

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
	ErrResourceNotFound        = responseError(gatewaymodel.CodeRouteNotFound, "gateway.resource_not_found", apperror.KindNotFound, "resource not found")
	ErrMethodNotAllowed        = responseErrorWithStatus(gatewaymodel.CodeMethodNotAllowed, http.StatusMethodNotAllowed, "gateway.method_not_allowed", apperror.KindInvalidArgument, "method not allowed")
	ErrRateLimited             = responseError(gatewaymodel.CodeRateLimited, "gateway.rate_limited", apperror.KindRateLimited, "rate limit exceeded")
	ErrUpstreamTimeout         = responseError(gatewaymodel.CodeUpstreamTimeout, "gateway.upstream_timeout", apperror.KindDeadlineExceeded, "upstream request timed out")
	ErrInvalidUpstreamResponse = responseErrorWithStatus(gatewaymodel.CodeInvalidUpstreamResponse, http.StatusBadGateway, "gateway.invalid_upstream_response", apperror.KindUnavailable, "invalid upstream response")
	ErrPreconditionFailed      = responseErrorWithStatus(gatewaymodel.CodePreconditionFailed, http.StatusPreconditionFailed, "gateway.precondition_failed", apperror.KindConflict, "resource revision does not match")
	ErrInternal                = responseError(gatewaymodel.CodeInternal, "gateway.internal", apperror.KindInternal, "internal server error")
)

// catalogHTTPStatus overrides Kind-derived HTTP status for protocol-specific
// codes that RFC mapping cannot express.
// catalogHTTPStatus 覆盖 kind 推导出的 HTTP 状态，用于 kind 无法表达的协议特例。
var catalogHTTPStatus = map[string]int{
	"identity.account_locked":           http.StatusLocked,
	"knowledge.gone":                    http.StatusGone,
	"knowledge.precondition_failed":     http.StatusPreconditionFailed,
	"collaboration.precondition_failed": http.StatusPreconditionFailed,
	"platform.precondition_failed":      http.StatusPreconditionFailed,
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
	writeRPCError(ctx, request, err)
}

func WriteKnowledgeError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err)
}

func WriteAttachmentError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err)
}

func WriteCollaborationError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err)
}

func WritePlatformError(ctx context.Context, request *app.RequestContext, err error) {
	writeRPCError(ctx, request, err)
}

func writeRPCError(ctx context.Context, request *app.RequestContext, err error) {
	if definition, ok := apperror.FromKitexBizStatus(err); ok {
		writeCatalogError(ctx, request, definition)
		return
	}
	if _, isBiz := kerrors.FromBizStatusError(err); isBiz {
		WriteError(ctx, request, ErrInvalidUpstreamResponse)
		return
	}
	if isTimeout(err) {
		WriteError(ctx, request, ErrUpstreamTimeout)
		return
	}
	WriteError(ctx, request, ErrDependencyUnavailable)
}

func writeCatalogError(ctx context.Context, request *app.RequestContext, definition apperror.Definition) {
	requestID, traceID := responseMetadata(ctx, request)
	problemContext := metadata.WithRequestID(ctx, requestID)
	status, payload := apperror.ToHTTPError(problemContext, definition.New())
	if override, found := catalogHTTPStatus[definition.Key]; found {
		status = override
		payload = apperror.ToHTTPProblem(problemContext, status, definition.New())
	}
	if traceID != nil {
		payload.TraceID = *traceID
	}
	request.Abort()
	writeProblem(request, status, payload)
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

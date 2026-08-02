package apperror

import (
	"context"
	"net/http"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const ProblemContentType = "application/problem+json; charset=utf-8"

// HTTPProblem is an RFC 9457 problem detail document. Code, key, request_id,
// and trace_id are stable extension members used by Knowledge Core clients.
type HTTPProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      int32  `json:"code"`
	Key       string `json:"key"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

// ToHTTPError converts an application error into a safe response payload and
// status code. Unknown errors never expose their message or wrapped cause.
func ToHTTPError(ctx context.Context, err error) (int, HTTPProblem) {
	definition, known := Details(err)
	if !known {
		definition = Internal
	}
	status := httpStatus(definition.Kind)
	return status, problem(ctx, status, definition)
}

// ToHTTPProblem converts an application error to a safe RFC 9457 document
// using an explicit HTTP status. It is intended for protocol-specific status
// mappings such as 423 Locked or 502 Bad Gateway.
func ToHTTPProblem(ctx context.Context, status int, err error) HTTPProblem {
	definition, known := Details(err)
	if !known {
		definition = Internal
		status = http.StatusInternalServerError
	}
	return problem(ctx, status, definition)
}

func problem(ctx context.Context, status int, definition Definition) HTTPProblem {
	return HTTPProblem{
		Type:      "urn:knowledge-core:problem:" + definition.Key,
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    definition.Message,
		Code:      definition.Code,
		Key:       definition.Key,
		RequestID: metadata.RequestID(ctx),
		TraceID:   traceIDFromContext(ctx),
	}
}

// WriteHertzError is the common Hertz error boundary. It uses the Sonic codec
// and mirrors request/trace identifiers in response headers and JSON.
func WriteHertzError(ctx context.Context, request *app.RequestContext, err error) {
	if request == nil || err == nil {
		return
	}
	status, response := ToHTTPError(ctx, err)
	if response.RequestID != "" {
		request.Header("X-Request-ID", response.RequestID)
	}
	if response.TraceID != "" {
		request.Header("X-Trace-ID", response.TraceID)
	}
	payload, marshalErr := jsoncodec.Marshal(response)
	if marshalErr != nil {
		request.Data(
			consts.StatusInternalServerError,
			ProblemContentType,
			[]byte(`{"type":"urn:knowledge-core:problem:common.internal","title":"Internal Server Error","status":500,"detail":"internal server error","code":1,"key":"common.internal"}`),
		)
		return
	}
	request.Data(status, ProblemContentType, payload)
}

func httpStatus(kind Kind) int {
	switch kind {
	case KindInvalidArgument:
		return consts.StatusBadRequest
	case KindUnauthenticated:
		return consts.StatusUnauthorized
	case KindPermissionDenied:
		return consts.StatusForbidden
	case KindNotFound:
		return consts.StatusNotFound
	case KindConflict:
		return consts.StatusConflict
	case KindRateLimited:
		return consts.StatusTooManyRequests
	case KindDeadlineExceeded:
		return consts.StatusGatewayTimeout
	case KindUnavailable:
		return consts.StatusServiceUnavailable
	case KindUnimplemented:
		return consts.StatusNotImplemented
	default:
		return consts.StatusInternalServerError
	}
}

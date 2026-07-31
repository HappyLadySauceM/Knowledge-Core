package apperror

import (
	"context"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type HTTPError struct {
	Code      int32  `json:"code"`
	Key       string `json:"key"`
	Kind      Kind   `json:"kind"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

type HTTPErrorResponse struct {
	Error HTTPError `json:"error"`
}

// ToHTTPError converts an application error into a safe response payload and
// status code. Unknown errors never expose their message or wrapped cause.
func ToHTTPError(ctx context.Context, err error) (int, HTTPErrorResponse) {
	definition, known := Details(err)
	if !known {
		definition = Internal
	}
	return httpStatus(definition.Kind), HTTPErrorResponse{Error: HTTPError{
		Code:      definition.Code,
		Key:       definition.Key,
		Kind:      definition.Kind,
		Message:   definition.Message,
		RequestID: metadata.RequestID(ctx),
		TraceID:   traceIDFromContext(ctx),
	}}
}

// WriteHertzError is the common Hertz error boundary. It uses the Sonic codec
// and mirrors request/trace identifiers in response headers and JSON.
func WriteHertzError(ctx context.Context, request *app.RequestContext, err error) {
	if request == nil || err == nil {
		return
	}
	status, response := ToHTTPError(ctx, err)
	if response.Error.RequestID != "" {
		request.Header("X-Request-ID", response.Error.RequestID)
	}
	if response.Error.TraceID != "" {
		request.Header("X-Trace-ID", response.Error.TraceID)
	}
	payload, marshalErr := jsoncodec.Marshal(response)
	if marshalErr != nil {
		request.Data(
			consts.StatusInternalServerError,
			consts.MIMEApplicationJSONUTF8,
			[]byte(`{"error":{"code":20999,"key":"common.internal","kind":"internal","message":"internal server error"}}`),
		)
		return
	}
	request.Data(status, consts.MIMEApplicationJSONUTF8, payload)
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

package apperror

import (
	"context"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"go.opentelemetry.io/otel/trace"
)

// ToBizStatus converts any error to a safe Kitex business status. Known
// application errors keep their catalog contract; unknown errors use Internal.
func ToBizStatus(ctx context.Context, err error) kerrors.BizStatusErrorIface {
	if err == nil {
		return nil
	}

	definition, known := Details(err)
	if !known {
		definition = Internal
	}
	extra := map[string]string{
		ExtraErrorKey:  definition.Key,
		ExtraErrorKind: string(definition.Kind),
	}
	if requestID := metadata.RequestID(ctx); requestID != "" {
		extra[ExtraRequestID] = requestID
	}
	if traceID := traceIDFromContext(ctx); traceID != "" {
		extra[ExtraTraceID] = traceID
	}

	biz := kerrors.NewBizStatusErrorWithExtra(definition.Code, definition.Message, extra)
	return &kitexBizError{BizStatusErrorIface: biz, cause: err}
}

// ToKitexBizStatus is an error-typed convenience for generated Kitex handlers.
func ToKitexBizStatus(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	return ToBizStatus(ctx, err)
}

func traceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

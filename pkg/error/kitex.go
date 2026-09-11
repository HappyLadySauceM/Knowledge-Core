package apperror

import (
	"context"
	"errors"

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
	var appError *Error
	if errors.As(err, &appError) && appError != nil {
		for key, value := range appError.extra {
			extra[key] = value
		}
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

// FromKitexBizStatus rebuilds a catalog definition from a Kitex business
// status.
// 从 Kitex 业务状态重建 catalog 定义。
// Only the serialized code, catalog message, error_key, and error_kind are
// used. Missing or invalid extras return false so callers do not guess from
// code alone.
// 只使用序列化的 code、catalog message、error_key 和 error_kind。extras 缺失或非法时返回 false，避免仅凭 code 猜测。
func FromKitexBizStatus(err error) (Definition, bool) {
	business, ok := kerrors.FromBizStatusError(err)
	if !ok || business == nil {
		return Definition{}, false
	}
	extra := business.BizExtra()
	definition, defineErr := Define(
		business.BizStatusCode(),
		extra[ExtraErrorKey],
		Kind(extra[ExtraErrorKind]),
		business.BizMessage(),
	)
	if defineErr != nil {
		return Definition{}, false
	}
	return definition, true
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

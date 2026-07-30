package observability

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type HertzIgnoreFunc func(context.Context, *app.RequestContext) bool

func HertzServerMiddleware(runtime *Runtime, ignore HertzIgnoreFunc) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if ignore != nil && ignore(ctx, request) {
			request.Next(ctx)
			return
		}

		ctx = otel.GetTextMapPropagator().Extract(ctx, hertzHeaderCarrier{header: &request.Request.Header})
		method := string(request.Method())
		ctx, span := runtime.Tracer("knowledge-core/hertz/server").Start(
			ctx,
			method,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attribute.String("http.request.method", method)),
		)
		defer span.End()
		if traceID := TraceID(ctx); traceID != "" {
			request.Header("X-Trace-ID", traceID)
		}

		request.Next(ctx)

		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := request.Response.StatusCode()
		span.SetName(method + " " + route)
		span.SetAttributes(
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
		)
		if requestID := RequestID(ctx); requestID != "" {
			span.SetAttributes(attribute.String("request.id", requestID))
		}
		if status >= 500 {
			span.SetStatus(codes.Error, "HTTP "+strconv.Itoa(status))
		}
	}
}

type hertzHeaderCarrier struct {
	header *protocol.RequestHeader
}

func (c hertzHeaderCarrier) Get(key string) string {
	return c.header.Get(key)
}

func (c hertzHeaderCarrier) Set(key, value string) {
	c.header.Set(key, value)
}

func (c hertzHeaderCarrier) Keys() []string {
	keys := make([]string, 0, c.header.Len())
	c.header.VisitAll(func(key, _ []byte) {
		keys = append(keys, string(key))
	})
	return keys
}

var _ propagation.TextMapCarrier = hertzHeaderCarrier{}

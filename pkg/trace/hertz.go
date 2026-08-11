package trace

import (
	"context"
	"strconv"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	RequestIDHeader = "X-Request-ID"
	TraceIDHeader   = "X-Trace-ID"
)

type HertzIgnoreFunc func(context.Context, *app.RequestContext) bool

func HertzServerMiddleware(runtime *Runtime, ignore HertzIgnoreFunc) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		ctx = otel.GetTextMapPropagator().Extract(ctx, hertzHeaderCarrier{header: &request.Request.Header})
		requestID := strings.TrimSpace(string(request.GetHeader(RequestIDHeader)))
		ctx = metadata.WithRequestID(ctx, requestID)
		ctx = metadata.EnsureRequestID(ctx)
		request.Header(RequestIDHeader, metadata.RequestID(ctx))
		if traceID := TraceID(ctx); traceID != "" {
			request.Header(TraceIDHeader, traceID)
		}
		// Low-value endpoints such as /metrics may opt out of span creation,
		// but still receive sanitized request metadata for logs and responses.
		path := string(request.Request.URI().Path())
		if IgnoreHTTPPath(path) || (ignore != nil && ignore(ctx, request)) {
			ctx = Suppress(ctx)
			request.Next(ctx)
			return
		}

		method := string(request.Method())
		ctx, span := tracer(runtime, "knowledge-core/hertz/server").Start(
			ctx,
			method,
			oteltrace.WithSpanKind(oteltrace.SpanKindServer),
			oteltrace.WithAttributes(
				attribute.String("http.request.method", method),
				attribute.String("request.id", metadata.RequestID(ctx)),
			),
		)
		defer span.End()

		if traceID := TraceID(ctx); traceID != "" {
			request.Header(TraceIDHeader, traceID)
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
		if status >= 500 {
			span.SetStatus(codes.Error, "HTTP "+strconv.Itoa(status))
		}
	}
}

type hertzHeaderCarrier struct {
	header *protocol.RequestHeader
}

func (c hertzHeaderCarrier) Get(key string) string { return c.header.Get(key) }
func (c hertzHeaderCarrier) Set(key, value string) { c.header.Set(key, value) }
func (c hertzHeaderCarrier) Keys() []string {
	keys := make([]string, 0, c.header.Len())
	c.header.VisitAll(func(key, _ []byte) {
		keys = append(keys, string(key))
	})
	return keys
}

func tracer(runtime *Runtime, name string) oteltrace.Tracer {
	if runtime == nil {
		return otel.Tracer(name)
	}
	return runtime.Tracer(name)
}

var _ propagation.TextMapCarrier = hertzHeaderCarrier{}

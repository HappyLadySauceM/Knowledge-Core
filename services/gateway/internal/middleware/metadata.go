package middleware

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/cloudwego/hertz/pkg/app"
	"go.opentelemetry.io/otel/trace"
)

const (
	requestIDKey = "knowledge-core.request-id"
	traceIDKey   = "knowledge-core.trace-id"
)

func RequestID() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		requestID := string(request.GetHeader("X-Request-ID"))
		if !validRequestID(requestID) {
			requestID = observability.NewRequestID()
		}
		request.Set(requestIDKey, requestID)
		request.Header("X-Request-ID", requestID)
		request.Next(observability.WithRequestID(ctx, requestID))
	}
}

// Trace composes the configured tracing middleware with request-local trace
// metadata. The second handler executes inside the span created by tracing.
func Trace(tracing app.HandlerFunc) []app.HandlerFunc {
	return []app.HandlerFunc{
		func(ctx context.Context, request *app.RequestContext) {
			request.Request.Header.Del("X-Trace-ID")
			request.Next(ctx)
		},
		tracing,
		func(ctx context.Context, request *app.RequestContext) {
			traceID := observability.TraceID(ctx)
			if !validTraceID(traceID) {
				spanContext := newLocalSpanContext()
				ctx = trace.ContextWithSpanContext(ctx, spanContext)
				traceID = spanContext.TraceID().String()
			}
			request.Set(traceIDKey, traceID)
			request.Header("X-Trace-ID", traceID)
			request.Next(ctx)
		},
	}
}

func AccessLog(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		started := time.Now()
		request.Next(ctx)

		logCtx := ctx
		if observability.RequestID(logCtx) == "" {
			logCtx = observability.WithRequestID(logCtx, GetRequestID(request))
		}
		if principal, authenticated := Principal(request); authenticated {
			logCtx = observability.WithUserID(logCtx, principal.UserID)
		}

		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := request.Response.StatusCode()
		level := slog.LevelInfo
		outcome := "success"
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
			outcome = "server_error"
		case status == http.StatusTooManyRequests:
			level = slog.LevelWarn
			outcome = "client_error"
		case status >= http.StatusBadRequest:
			outcome = "client_error"
		case strings.HasPrefix(route, "/health/"):
			level = slog.LevelDebug
		}
		logger.LogAttrs(logCtx, level, "HTTP request",
			slog.String("component", "hertz"),
			slog.String("event", "http_request"),
			slog.String("http_method", string(request.Method())),
			slog.String("http_route", route),
			slog.Int("http_status", status),
			slog.String("client_ip", normalizeClientIP(request.ClientIP())),
			slog.String("outcome", outcome),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.Int("response_bytes", len(request.Response.Body())),
		)
	}
}

func GetRequestID(request *app.RequestContext) string {
	value, exists := request.Get(requestIDKey)
	if requestID, ok := value.(string); exists && ok {
		return requestID
	}
	return ""
}

func GetTraceID(request *app.RequestContext) string {
	value, exists := request.Get(traceIDKey)
	if traceID, ok := value.(string); exists && ok {
		return traceID
	}
	return ""
}

func TraceIDPointer(request *app.RequestContext) *string {
	traceID := GetTraceID(request)
	return &traceID
}

func ensureResponseMetadata(request *app.RequestContext) (string, string) {
	requestID := GetRequestID(request)
	if !validRequestID(requestID) {
		requestID = observability.NewRequestID()
		request.Set(requestIDKey, requestID)
	}
	traceID := GetTraceID(request)
	if !validTraceID(traceID) {
		traceID = newLocalSpanContext().TraceID().String()
		request.Set(traceIDKey, traceID)
	}
	request.Header("X-Request-ID", requestID)
	request.Header("X-Trace-ID", traceID)
	return requestID, traceID
}

func validRequestID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validTraceID(value string) bool {
	parsed, err := trace.TraceIDFromHex(value)
	return err == nil && parsed.IsValid()
}

func newLocalSpanContext() trace.SpanContext {
	var seed [24]byte
	if _, err := rand.Read(seed[:]); err != nil {
		fallback := sha256.Sum256([]byte(observability.NewRequestID()))
		copy(seed[:], fallback[:])
	}
	var traceID trace.TraceID
	var spanID trace.SpanID
	copy(traceID[:], seed[:16])
	copy(spanID[:], seed[16:])
	if !traceID.IsValid() {
		traceID[15] = 1
	}
	if !spanID.IsValid() {
		spanID[7] = 1
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID})
}

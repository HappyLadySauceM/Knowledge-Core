package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/cache"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/hlog"
)

var allowedCORSMethods = map[string]struct{}{
	http.MethodGet: {}, http.MethodPost: {}, http.MethodPatch: {}, http.MethodDelete: {}, http.MethodOptions: {},
}

var allowedCORSHeaders = map[string]struct{}{
	"authorization": {}, "content-type": {}, "x-request-id": {},
	"traceparent": {}, "tracestate": {}, "baggage": {},
}

const (
	allowedMethodsHeader = "GET, POST, PATCH, DELETE, OPTIONS"
	allowedHeadersHeader = "Authorization, Content-Type, X-Request-ID, traceparent, tracestate, baggage"
	exposedHeadersHeader = "X-Request-ID, X-Trace-ID, Retry-After"
)

func JSONRecovery(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		defer func() {
			if recovered := recover(); recovered != nil {
				writePanicResponse(logger, ctx, request, "handler", fmt.Sprintf("%T", recovered))
			}
		}()
		request.Next(ctx)
	}
}

// JSONPanicHandler is the engine-level fallback for panics in middleware that
// necessarily runs before JSONRecovery in the approved middleware order.
func JSONPanicHandler(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		writePanicResponse(logger, ctx, request, "outer_middleware", "")
	}
}

func SecurityHeaders() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		setSecurityHeaders(request)
		request.Next(ctx)
	}
}

func CORS(cfg CORSConfig, trustedProxyCIDRs []*net.IPNet) app.HandlerFunc {
	allowedOrigins := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, origin := range cfg.AllowedOrigins {
		allowedOrigins[origin] = struct{}{}
	}
	return func(ctx context.Context, request *app.RequestContext) {
		origin := strings.TrimSpace(string(request.GetHeader("Origin")))
		if origin == "" {
			request.Next(ctx)
			return
		}
		request.Header("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		_, explicitlyAllowed := allowedOrigins[origin]
		if !explicitlyAllowed && !sameOrigin(request, origin, trustedProxyCIDRs) {
			WriteError(request, ErrPermissionDenied)
			return
		}

		request.Header("Access-Control-Allow-Origin", origin)
		request.Header("Access-Control-Expose-Headers", exposedHeadersHeader)
		if string(request.Method()) != http.MethodOptions || len(request.GetHeader("Access-Control-Request-Method")) == 0 {
			request.Next(ctx)
			return
		}

		requestedMethod := strings.ToUpper(strings.TrimSpace(string(request.GetHeader("Access-Control-Request-Method"))))
		if _, allowed := allowedCORSMethods[requestedMethod]; !allowed || !corsHeadersAllowed(string(request.GetHeader("Access-Control-Request-Headers"))) {
			WriteError(request, ErrPermissionDenied)
			return
		}
		request.Header("Access-Control-Allow-Methods", allowedMethodsHeader)
		request.Header("Access-Control-Allow-Headers", allowedHeadersHeader)
		request.AbortWithStatus(http.StatusNoContent)
	}
}

func RateLimit(store cache.KVStore, cfg RateLimitConfig) app.HandlerFunc {
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context, request *app.RequestContext) {
		if isHealthProbe(request) {
			request.Next(ctx)
			return
		}
		currentTime := now().UTC()
		clientIP := normalizeClientIP(request.ClientIP())
		if !consumeRateLimit(ctx, request, store, "global", clientIP, currentTime, cfg.Window, cfg.GlobalLimit) {
			return
		}
		if isAuthenticationEndpoint(request) && !consumeRateLimit(ctx, request, store, "auth", clientIP, currentTime, cfg.Window, cfg.AuthLimit) {
			return
		}
		request.Next(ctx)
	}
}

func isHealthProbe(request *app.RequestContext) bool {
	if string(request.Method()) != http.MethodGet {
		return false
	}
	switch string(request.Path()) {
	case "/health/live", "/health/ready":
		return true
	default:
		return false
	}
}

func NoRoute() app.HandlerFunc {
	return func(_ context.Context, request *app.RequestContext) {
		WriteError(request, ErrRouteNotFound)
	}
}

func NoMethod() app.HandlerFunc {
	return func(_ context.Context, request *app.RequestContext) {
		WriteError(request, ErrMethodNotAllowed)
	}
}

func consumeRateLimit(
	ctx context.Context,
	request *app.RequestContext,
	store cache.KVStore,
	scope string,
	clientIP string,
	now time.Time,
	window time.Duration,
	limit int64,
) bool {
	windowNanos := int64(window)
	bucket := now.UnixNano() / windowNanos
	resetAt := time.Unix(0, (bucket+1)*windowNanos).UTC()
	remaining := resetAt.Sub(now)
	if remaining <= 0 {
		remaining = window
	}
	key := fmt.Sprintf("gateway:rate-limit:%s:%s:%d", scope, clientIP, bucket)
	ttl := remaining
	if ttl < time.Millisecond {
		ttl = time.Millisecond
	}
	count, err := store.Increment(ctx, key, 1, ttl)
	if err != nil {
		hlog.CtxErrorf(ctx, "rate limit cache increment failed: %v", err)
		WriteError(request, ErrDependencyUnavailable)
		return false
	}
	if count <= limit {
		return true
	}
	retryAfter := remaining / time.Second
	if remaining%time.Second != 0 {
		retryAfter++
	}
	request.Header("Retry-After", strconv.FormatInt(int64(retryAfter), 10))
	WriteError(request, ErrRateLimited)
	return false
}

func isAuthenticationEndpoint(request *app.RequestContext) bool {
	if string(request.Method()) != http.MethodPost {
		return false
	}
	switch string(request.Path()) {
	case "/api/v1/auth/login", "/api/v1/auth/register":
		return true
	default:
		return false
	}
}

func sameOrigin(request *app.RequestContext, origin string, trustedProxyCIDRs []*net.IPNet) bool {
	scheme := effectiveRequestScheme(request, trustedProxyCIDRs)
	host := string(request.Host())
	return scheme != "" && host != "" && origin == scheme+"://"+host
}

func effectiveRequestScheme(request *app.RequestContext, trustedProxyCIDRs []*net.IPNet) string {
	scheme := string(request.Request.Scheme())
	if !trustedRemoteAddress(request.RemoteAddr(), trustedProxyCIDRs) {
		return scheme
	}
	forwarded := string(request.GetHeader("X-Forwarded-Proto"))
	if strings.ContainsRune(forwarded, ',') {
		return scheme
	}
	forwarded = strings.ToLower(strings.TrimSpace(forwarded))
	if forwarded == "http" || forwarded == "https" {
		return forwarded
	}
	return scheme
}

func trustedRemoteAddress(address net.Addr, trustedProxyCIDRs []*net.IPNet) bool {
	if address == nil || len(trustedProxyCIDRs) == 0 {
		return false
	}
	host := "127.0.0.1"
	if !strings.HasPrefix(address.Network(), "unix") {
		var err error
		host, _, err = net.SplitHostPort(strings.TrimSpace(address.String()))
		if err != nil {
			return false
		}
	}
	remoteIP := net.ParseIP(host)
	if remoteIP == nil {
		return false
	}
	for _, network := range trustedProxyCIDRs {
		if network != nil && network.Contains(remoteIP) {
			return true
		}
	}
	return false
}

func corsHeadersAllowed(raw string) bool {
	for _, header := range strings.Split(raw, ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "" {
			continue
		}
		if _, allowed := allowedCORSHeaders[header]; !allowed {
			return false
		}
	}
	return true
}

func normalizeClientIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	ip := net.ParseIP(strings.Trim(raw, "[]"))
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}

func writePanicResponse(logger *slog.Logger, ctx context.Context, request *app.RequestContext, origin, panicType string) {
	requestID, traceID := ensureResponseMetadata(request)
	attributes := []any{
		"component", "hertz",
		"event", "panic_recovery",
		"panic_origin", origin,
		"request_id", requestID,
		"trace_id", traceID,
		"stack", string(debug.Stack()),
	}
	if panicType != "" {
		attributes = append(attributes, "panic_type", panicType)
	}
	logger.ErrorContext(ctx, "recovered HTTP panic", attributes...)
	request.Response.ResetBody()
	setSecurityHeaders(request)
	WriteError(request, ErrInternal)
}

func setSecurityHeaders(request *app.RequestContext) {
	request.Header("X-Content-Type-Options", "nosniff")
	request.Header("X-Frame-Options", "DENY")
	request.Header("Referrer-Policy", "no-referrer")
	request.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
}

package middleware

import (
	"context"
	"log/slog"
	"net"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func Recovery() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if dependencies, ok := FromRequest(request); ok {
					dependencies.Logger.ErrorContext(ctx, "HTTP handler panic recovered",
						slog.String("component", "gateway.public"),
						slog.String("event", "panic"),
						slog.Any("panic", recovered),
						slog.String("stack", string(debug.Stack())),
					)
				}
				WriteError(ctx, request, ErrInternal)
			}
		}()
		request.Next(ctx)
	}
}

func AccessLog() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		started := time.Now()
		request.Next(ctx)
		dependencies, ok := FromRequest(request)
		if !ok {
			return
		}
		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		dependencies.Logger.InfoContext(ctx, "HTTP request completed",
			slog.String("component", "gateway.public"),
			slog.String("event", "request"),
			slog.String("http.method", string(request.Method())),
			slog.String("http.route", route),
			slog.Int("http.status_code", request.Response.StatusCode()),
			slog.String("client.address", ClientIP(request, dependencies.TrustedProxies)),
			slog.Duration("duration", time.Since(started)),
		)
	}
}

func SecurityHeaders() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		request.Header("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		request.Header("Referrer-Policy", "no-referrer")
		request.Header("X-Content-Type-Options", "nosniff")
		request.Header("X-Frame-Options", "DENY")
		if dependencies, ok := FromRequest(request); ok && dependencies.Secure {
			request.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		request.Next(ctx)
	}
}

func CORS() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		origins := request.Request.Header.PeekAll("Origin")
		if len(origins) == 0 {
			request.Next(ctx)
			return
		}
		if len(origins) != 1 || strings.TrimSpace(string(origins[0])) != string(origins[0]) || len(origins[0]) == 0 {
			WriteError(ctx, request, ErrInvalidRequest)
			return
		}
		origin := string(origins[0])
		dependencies, ok := FromRequest(request)
		if !ok {
			WriteError(ctx, request, ErrInternal)
			return
		}
		if _, allowed := dependencies.AllowedOrigins[origin]; !allowed {
			WriteError(ctx, request, ErrPermissionDenied)
			return
		}
		request.Header("Access-Control-Allow-Origin", origin)
		request.Header("Access-Control-Expose-Headers", "ETag, Location, X-Request-ID, X-Trace-ID")
		request.Header("Vary", "Origin")
		if string(request.Method()) == consts.MethodOptions && request.GetHeader("Access-Control-Request-Method") != nil {
			request.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, If-Match, X-Request-ID, traceparent, tracestate")
			request.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			request.Header("Access-Control-Max-Age", "600")
			request.Abort()
			request.Status(consts.StatusNoContent)
			return
		}
		request.Next(ctx)
	}
}

func OptionalAuthentication() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		headers := request.Request.Header.PeekAll("Authorization")
		if len(headers) == 0 {
			request.Next(ctx)
			return
		}
		if len(headers) != 1 {
			request.Header("WWW-Authenticate", "Bearer")
			WriteError(ctx, request, ErrAuthenticationRequired)
			return
		}
		header := string(headers[0])
		scheme, token, found := strings.Cut(header, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") ||
			len(token) > coreauth.MaxTokenLength {
			request.Header("WWW-Authenticate", "Bearer")
			WriteError(ctx, request, ErrAuthenticationRequired)
			return
		}
		dependencies, ok := FromRequest(request)
		if !ok {
			WriteError(ctx, request, ErrInternal)
			return
		}
		principal, err := dependencies.Verifier.Verify(token)
		if err != nil {
			request.Header("WWW-Authenticate", "Bearer")
			WriteError(ctx, request, ErrAuthenticationRequired)
			return
		}
		request.Set(principalKey, principal)
		request.Set(accessTokenKey, token)
		request.Next(metadata.WithUserID(ctx, principal.UserID))
	}
}

func RequireAuthenticated() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if _, ok := Principal(request); !ok {
			request.Header("WWW-Authenticate", "Bearer")
			WriteError(ctx, request, ErrAuthenticationRequired)
			return
		}
		request.Next(ctx)
	}
}

func GlobalRateLimit() app.HandlerFunc {
	return rateLimit("global", func(options configRateLimit) int64 { return options.global })
}

func AuthRateLimit() app.HandlerFunc {
	return rateLimit("auth", func(options configRateLimit) int64 { return options.auth })
}

func CollaborationSessionRateLimit() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		dependencies, ok := FromRequest(request)
		principal, principalOK := Principal(request)
		if !ok || !principalOK {
			WriteError(ctx, request, ErrInternal)
			return
		}
		now := time.Now()
		if dependencies.Now != nil {
			now = dependencies.Now()
		}
		for _, subject := range []struct {
			scope string
			key   string
		}{
			{scope: "collaboration_session_ip", key: ClientIP(request, dependencies.TrustedProxies)},
			{scope: "collaboration_session_user", key: "user:" + strconv.FormatInt(principal.UserID, 10)},
		} {
			if !consumeRateLimit(ctx, request, dependencies, subject.scope, subject.key, now, dependencies.RateLimit.AuthLimit) {
				return
			}
		}
		request.Next(ctx)
	}
}

type configRateLimit struct {
	global int64
	auth   int64
}

func rateLimit(scope string, limit func(configRateLimit) int64) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		dependencies, ok := FromRequest(request)
		if !ok {
			WriteError(ctx, request, ErrInternal)
			return
		}
		now := time.Now()
		if dependencies.Now != nil {
			now = dependencies.Now()
		}
		if !consumeRateLimit(
			ctx, request, dependencies, scope, ClientIP(request, dependencies.TrustedProxies), now,
			limit(configRateLimit{global: dependencies.RateLimit.GlobalLimit, auth: dependencies.RateLimit.AuthLimit}),
		) {
			return
		}
		request.Next(ctx)
	}
}

func consumeRateLimit(
	ctx context.Context,
	request *app.RequestContext,
	dependencies *Dependencies,
	scope string,
	subject string,
	now time.Time,
	limit int64,
) bool {
	allowed, retryAfter, err := dependencies.Limiter.Consume(
		ctx, scope, subject, now, dependencies.RateLimit.Window, limit,
	)
	if err != nil {
		dependencies.Logger.ErrorContext(ctx, "gateway rate limiter failed",
			slog.String("component", "gateway.rate_limit"),
			slog.String("event", "dependency_error"),
			slog.String("scope", scope),
			slog.Any("error", err),
		)
		WriteError(ctx, request, ErrDependencyUnavailable)
		return false
	}
	if !allowed {
		seconds := int64((retryAfter + time.Second - 1) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		request.Header("Retry-After", strconv.FormatInt(seconds, 10))
		WriteError(ctx, request, ErrRateLimited)
		return false
	}
	return true
}

func ClientIP(request *app.RequestContext, trustedProxies []*net.IPNet) string {
	if request == nil || request.RemoteAddr() == nil {
		return "unknown"
	}
	peer := addressIP(request.RemoteAddr().String())
	if peer == nil {
		return "unknown"
	}
	request.Set(clientIPKey, peer.String())
	if !containsIP(trustedProxies, peer) {
		return peer.String()
	}
	forwarded := strings.Split(string(request.GetHeader("X-Forwarded-For")), ",")
	var leftmost net.IP
	for index := len(forwarded) - 1; index >= 0; index-- {
		candidate := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if candidate == nil {
			continue
		}
		leftmost = candidate
		if !containsIP(trustedProxies, candidate) {
			return candidate.String()
		}
	}
	if leftmost != nil {
		return leftmost.String()
	}
	if realIP := net.ParseIP(strings.TrimSpace(string(request.GetHeader("X-Real-IP")))); realIP != nil {
		return realIP.String()
	}
	return peer.String()
}

func addressIP(address string) net.IP {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func containsIP(networks []*net.IPNet, ip net.IP) bool {
	for _, network := range networks {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

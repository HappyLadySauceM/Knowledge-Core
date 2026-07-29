package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/cloudwego/hertz/pkg/app"
)

const (
	healthRegistryKey = "knowledge-core.health-registry"
	identityClientKey = "knowledge-core.identity-client"
	principalKey      = "knowledge-core.auth-principal"
	requestIDKey      = "knowledge-core.request-id"
	codeUnauthorized  = int32(10003)
	codeForbidden     = int32(10005)
)

type RuntimeDependencies struct {
	Health   *health.Registry
	Identity identityservice.Client
}

type TokenVerifier interface {
	Verify(value string) (auth.Principal, error)
}

func Dependencies(dependencies RuntimeDependencies) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(healthRegistryKey, dependencies.Health)
		request.Set(identityClientKey, dependencies.Identity)
		request.Next(ctx)
	}
}

func Authentication(verifier TokenVerifier) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		header := string(request.GetHeader("Authorization"))
		if header != "" && verifier != nil {
			parts := strings.Fields(header)
			if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
				if principal, err := verifier.Verify(parts[1]); err == nil {
					request.Set(principalKey, principal)
					ctx = observability.WithUserID(ctx, principal.UserID)
				}
			}
		}
		request.Next(ctx)
	}
}

func RequestID() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		requestID := string(request.GetHeader("X-Request-ID"))
		if !validRequestID(requestID) {
			requestID = observability.NewRequestID()
		}
		request.Set(requestIDKey, requestID)
		request.Header("X-Request-ID", requestID)
		ctx = observability.WithRequestID(ctx, requestID)
		request.Next(ctx)
	}
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

func AccessLog(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		started := time.Now()
		request.Next(ctx)
		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := request.Response.StatusCode()
		level := slog.LevelInfo
		outcome := "success"
		switch {
		case strings.HasPrefix(route, "/health/"):
			level = slog.LevelDebug
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
			outcome = "server_error"
		case status == http.StatusTooManyRequests:
			level = slog.LevelWarn
			outcome = "client_error"
		case status >= http.StatusBadRequest:
			outcome = "client_error"
		}
		logger.LogAttrs(ctx, level, "HTTP request",
			slog.String("component", "hertz"),
			slog.String("event", "http_request"),
			slog.String("http_method", string(request.Method())),
			slog.String("http_route", route),
			slog.Int("http_status", status),
			slog.String("outcome", outcome),
			slog.Int64("duration_ms", time.Since(started).Milliseconds()),
			slog.Int("response_bytes", len(request.Response.Body())),
		)
	}
}

func RequireAuthenticated() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if _, authenticated := Principal(request); !authenticated {
			writeAuthorizationError(request, http.StatusUnauthorized, codeUnauthorized, "authentication required")
			return
		}
		request.Next(ctx)
	}
}

func RequireRoles(roles ...string) app.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if role = strings.TrimSpace(role); role != "" {
			allowed[role] = struct{}{}
		}
	}
	return func(ctx context.Context, request *app.RequestContext) {
		principal, authenticated := Principal(request)
		if !authenticated {
			writeAuthorizationError(request, http.StatusUnauthorized, codeUnauthorized, "authentication required")
			return
		}
		if _, exists := allowed[principal.Role]; !exists {
			writeAuthorizationError(request, http.StatusForbidden, codeForbidden, "permission denied")
			return
		}
		request.Next(ctx)
	}
}

func HealthRegistry(request *app.RequestContext) (*health.Registry, bool) {
	value, exists := request.Get(healthRegistryKey)
	registry, ok := value.(*health.Registry)
	return registry, exists && ok
}

func IdentityClient(request *app.RequestContext) (identityservice.Client, bool) {
	value, exists := request.Get(identityClientKey)
	client, ok := value.(identityservice.Client)
	return client, exists && ok && client != nil
}

func Principal(request *app.RequestContext) (auth.Principal, bool) {
	value, exists := request.Get(principalKey)
	principal, ok := value.(auth.Principal)
	return principal, exists && ok && principal.UserID > 0
}

func GetRequestID(request *app.RequestContext) string {
	value, exists := request.Get(requestIDKey)
	if requestID, ok := value.(string); exists && ok {
		return requestID
	}
	return ""
}

func writeAuthorizationError(request *app.RequestContext, status int, code int32, message string) {
	request.AbortWithStatusJSON(status, &gatewaymodel.ErrorResponse{
		Code:      code,
		Message:   message,
		Data:      &gatewaymodel.EmptyData{},
		RequestID: GetRequestID(request),
	})
}

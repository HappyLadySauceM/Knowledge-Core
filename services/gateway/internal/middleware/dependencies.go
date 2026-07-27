package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	foundationauth "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/health"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/cloudwego/hertz/pkg/app"
)

const (
	healthRegistryKey = "knowledge-core.health-registry"
	identityClientKey = "knowledge-core.identity-client"
	principalKey      = "knowledge-core.auth-principal"
	requestIDKey      = "knowledge-core.request-id"
)

type RuntimeDependencies struct {
	Health   *health.Registry
	Identity identityservice.Client
}

type TokenVerifier interface {
	Verify(value string) (foundationauth.Principal, error)
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
				}
			}
		}
		request.Next(ctx)
	}
}

func RequestID() app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		requestID := string(request.GetHeader("X-Request-ID"))
		if requestID == "" || len(requestID) > 64 {
			requestID = newRequestID()
		}
		request.Set(requestIDKey, requestID)
		request.Header("X-Request-ID", requestID)
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

func Principal(request *app.RequestContext) (foundationauth.Principal, bool) {
	value, exists := request.Get(principalKey)
	principal, ok := value.(foundationauth.Principal)
	return principal, exists && ok && principal.UserID > 0
}

func GetRequestID(request *app.RequestContext) string {
	value, exists := request.Get(requestIDKey)
	if requestID, ok := value.(string); exists && ok {
		return requestID
	}
	return ""
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("fallback-%d", time.Now().UTC().UnixNano())
}

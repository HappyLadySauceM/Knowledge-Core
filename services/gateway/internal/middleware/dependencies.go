package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/health"
	"github.com/cloudwego/hertz/pkg/app"
)

const (
	healthRegistryKey = "knowledge-core.health-registry"
	requestIDKey      = "knowledge-core.request-id"
)

func Dependencies(registry *health.Registry) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		request.Set(healthRegistryKey, registry)
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

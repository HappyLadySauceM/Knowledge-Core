package trace

import (
	"context"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// MaxBaggageLength bounds metadata persisted in an outbox or sent through a
// broker. Baggage is diagnostic context, never an application payload.
const MaxBaggageLength = 8_192

// PropagationHeaders returns the legal W3C trace context and baggage fields
// for durable transport headers. It includes the bounded request ID when one
// is present, but never authentication material.
func PropagationHeaders(ctx context.Context) map[string]string {
	if ctx == nil {
		ctx = context.Background()
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	headers := make(map[string]string, len(carrier))
	for key, value := range carrier {
		if strings.EqualFold(key, "baggage") && len(value) > MaxBaggageLength {
			continue
		}
		headers[strings.ToLower(key)] = value
	}
	if requestID := metadata.RequestID(ctx); requestID != "" {
		headers["x-request-id"] = requestID
	}
	return headers
}

func ContextFromPropagation(ctx context.Context, headers map[string]string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	carrier := propagation.MapCarrier{}
	for key, value := range headers {
		if strings.EqualFold(key, "baggage") && len(value) > MaxBaggageLength {
			continue
		}
		carrier.Set(key, value)
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

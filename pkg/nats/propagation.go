package nats

import (
	"context"
	"strings"

	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	natsclient "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// tracePropagator carries W3C trace and baggage headers across NATS messages.
var tracePropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

type headerCarrier natsclient.Header

func (c headerCarrier) Get(key string) string { return natsclient.Header(c).Get(key) }
func (c headerCarrier) Set(key, value string) { natsclient.Header(c).Set(key, value) }
func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

func injectTrace(ctx context.Context, headers natsclient.Header) {
	carrier := propagation.MapCarrier{}
	tracePropagator.Inject(ctx, carrier)
	for key, value := range carrier {
		if strings.EqualFold(key, "baggage") && len(value) > coretrace.MaxBaggageLength {
			continue
		}
		headers.Set(key, value)
	}
}

func extractTrace(ctx context.Context, headers natsclient.Header) context.Context {
	if value := headers.Get("baggage"); len(value) > coretrace.MaxBaggageLength {
		headers.Del("baggage")
	}
	return tracePropagator.Extract(ctx, headerCarrier(headers))
}

func startMessageSpan(ctx context.Context, kind string, messageID, subject string, spanKind oteltrace.SpanKind) (context.Context, oteltrace.Span) {
	// Kept as a small helper so publish and delivery use identical bounded
	// attributes without ever recording message payloads or user identifiers.
	if coretrace.IsSuppressed(ctx) {
		return ctx, oteltrace.SpanFromContext(ctx)
	}
	return otel.Tracer("knowledge-core/nats").Start(ctx, kind+" "+subject,
		oteltrace.WithSpanKind(spanKind),
		oteltrace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", subject),
			attribute.String("messaging.message.id", messageID),
		),
	)
}

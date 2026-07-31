package nats

import (
	"context"

	natsclient "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/propagation"
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
	tracePropagator.Inject(ctx, headerCarrier(headers))
}

func extractTrace(ctx context.Context, headers natsclient.Header) context.Context {
	return tracePropagator.Extract(ctx, headerCarrier(headers))
}

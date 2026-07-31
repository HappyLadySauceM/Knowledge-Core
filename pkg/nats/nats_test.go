package nats

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	natsclient "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextRoundTrip(t *testing.T) {
	t.Parallel()
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	member, err := baggage.NewMember("tenant", "public")
	if err != nil {
		t.Fatalf("baggage.NewMember() error = %v", err)
	}
	values, err := baggage.New(member)
	if err != nil {
		t.Fatalf("baggage.New() error = %v", err)
	}
	ctx = baggage.ContextWithBaggage(ctx, values)
	headers := natsclient.Header{}
	injectTrace(ctx, headers)
	if headers.Get("traceparent") == "" {
		t.Fatal("traceparent was not injected")
	}
	if headers.Get("baggage") != "tenant=public" {
		t.Fatalf("baggage header = %q", headers.Get("baggage"))
	}
	extracted := trace.SpanContextFromContext(extractTrace(context.Background(), headers))
	if extracted.TraceID() != spanContext.TraceID() || extracted.SpanID() != spanContext.SpanID() {
		t.Fatalf("extracted span context = %v, want %v", extracted, spanContext)
	}
	if !extracted.IsRemote() {
		t.Fatal("extracted span context is not remote")
	}
}

func TestConnectRejectsInvalidOptionsBeforeDialing(t *testing.T) {
	t.Parallel()
	opts := *option.NewNATSOptions()
	opts.Servers = nil
	_, err := OpenRealtime(context.Background(), opts, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("OpenRealtime() error = %v, want invalid options", err)
	}
}

func TestConnectionFailureIsBounded(t *testing.T) {
	t.Parallel()
	opts := *option.NewNATSOptions()
	opts.Servers = []string{"nats://127.0.0.1:1"}
	opts.ConnectTimeout = 25 * time.Millisecond
	opts.MaxReconnects = 0
	_, err := OpenDurable(context.Background(), opts, nil)
	if err == nil || !strings.Contains(err.Error(), "connect nats") {
		t.Fatalf("OpenDurable() error = %v, want connect nats failure", err)
	}
}

func TestValidateConsumerAggregatesErrors(t *testing.T) {
	t.Parallel()
	err := validateConsumer(ConsumerConfig{AckWait: -1, MaxDeliver: -1}, nil)
	if err == nil {
		t.Fatal("validateConsumer() error = nil")
	}
	for _, expected := range []string{"stream is required", "durable name is required", "subject is required", "ack wait", "max deliver", "handler is required"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("validateConsumer() error = %q, want %q", err, expected)
		}
	}
}

func TestNilBrokerLifecycle(t *testing.T) {
	t.Parallel()
	var durable *DurableBroker
	if err := durable.Close(); err != nil {
		t.Fatalf("DurableBroker.Close() error = %v", err)
	}
	if err := durable.Ping(context.Background()); err == nil {
		t.Fatal("DurableBroker.Ping() error = nil")
	}
	var realtime *RealtimeBus
	if err := realtime.Close(); err != nil {
		t.Fatalf("RealtimeBus.Close() error = %v", err)
	}
}

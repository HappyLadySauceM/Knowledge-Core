package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"go.opentelemetry.io/otel/trace"
)

func TestLoggerIncludesServiceContextAndRedactsSecrets(t *testing.T) {
	var output bytes.Buffer
	runtime, err := observability.New(context.Background(), observability.Config{
		Service: "gateway", Environment: "test", Level: "info", Output: &output, SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	ctx = observability.WithRequestID(ctx, "request-1")
	ctx = observability.WithUserID(ctx, 42)
	runtime.Logger().InfoContext(ctx, "started", "address", ":8080", "password", "secret", "database.dsn", "postgres://secret")

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("Unmarshal() error = %v; output = %q", err, output.String())
	}
	want := map[string]any{
		"service": "gateway", "environment": "test", "msg": "started", "request_id": "request-1",
		"trace_id": traceID.String(), "span_id": spanID.String(), "user_id": float64(42), "password": "[REDACTED]",
		"database.dsn": "[REDACTED]",
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("record[%q] = %#v, want %#v", key, record[key], value)
		}
	}
}

func TestLoggerFiltersByLevel(t *testing.T) {
	var output bytes.Buffer
	runtime, err := observability.New(context.Background(), observability.Config{
		Service: "identity", Environment: "test", Level: "warn", Output: &output, SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runtime.Logger().Info("hidden")
	runtime.Logger().Warn("visible")
	if bytes.Contains(output.Bytes(), []byte("hidden")) || !bytes.Contains(output.Bytes(), []byte("visible")) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestObservabilityConfigRejectsInvalidValues(t *testing.T) {
	tests := []observability.Config{
		{Environment: "test", Level: "info", SampleRatio: 1},
		{Service: "gateway", Environment: "test", Level: "verbose", SampleRatio: 1},
		{Service: "gateway", Environment: "test", Level: "info", SampleRatio: 2},
		{Service: "gateway", Environment: "test", Level: "info", SampleRatio: 1, OTLPEndpoint: "collector:4317"},
	}
	for _, config := range tests {
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", config)
		}
	}
}

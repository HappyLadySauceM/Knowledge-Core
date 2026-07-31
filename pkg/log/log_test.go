package log_test

import (
	"bytes"
	"context"
	stdjson "encoding/json"
	"log/slog"
	"testing"

	logpkg "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"go.opentelemetry.io/otel/trace"
)

func TestLoggerAddsContextAndRecursivelyRedacts(t *testing.T) {
	var output bytes.Buffer
	logger, _, err := logpkg.New("identity", "test", "debug", &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	ctx = metadata.WithRequestID(ctx, "request-1")
	ctx = metadata.WithUserID(ctx, 42)

	logger.InfoContext(ctx, "registered",
		slog.Any("input", map[string]any{
			"email": "member@example.com",
			"auth":  map[string]any{"access_token": "must-not-appear"},
		}),
	)

	var entry map[string]any
	if err := stdjson.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if entry["service"] != "identity" || entry["environment"] != "test" || entry["request_id"] != "request-1" {
		t.Fatalf("entry metadata = %#v", entry)
	}
	if entry["trace_id"] != traceID.String() || entry["span_id"] != spanID.String() || entry["user_id"] != float64(42) {
		t.Fatalf("entry trace metadata = %#v", entry)
	}
	input := entry["input"].(map[string]any)
	auth := input["auth"].(map[string]any)
	if auth["access_token"] != "[REDACTED]" {
		t.Fatalf("nested token = %#v", auth["access_token"])
	}
	if bytes.Contains(output.Bytes(), []byte("must-not-appear")) {
		t.Fatal("log output leaked a sensitive value")
	}
}

func TestDynamicLevel(t *testing.T) {
	var output bytes.Buffer
	logger, level, err := logpkg.New("identity", "test", "info", &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Debug("hidden")
	if output.Len() != 0 {
		t.Fatalf("debug log was emitted at info level: %q", output.String())
	}
	if err := logpkg.SetLevel(level, "debug"); err != nil {
		t.Fatalf("SetLevel() error = %v", err)
	}
	logger.Debug("visible")
	if !bytes.Contains(output.Bytes(), []byte("visible")) {
		t.Fatalf("debug log was not emitted: %q", output.String())
	}
}

func TestLoggerRejectsInvalidConfiguration(t *testing.T) {
	for _, test := range []struct{ service, environment, level string }{
		{environment: "test", level: "info"},
		{service: "identity", level: "info"},
		{service: "identity", environment: "test", level: "verbose"},
	} {
		if _, _, err := logpkg.New(test.service, test.environment, test.level, nil); err == nil {
			t.Fatalf("New(%q, %q, %q) accepted invalid config", test.service, test.environment, test.level)
		}
	}
}

func TestNewWithOptionsAddsSourceToJSON(t *testing.T) {
	var output bytes.Buffer
	logger, _, err := logpkg.NewWithOptions(logpkg.Options{
		Service: "identity", Environment: "test", Level: "info",
		AddSource: true, Output: &output,
	})
	if err != nil {
		t.Fatalf("NewWithOptions() error = %v", err)
	}
	logger.Info("ready")
	var entry map[string]any
	if err := stdjson.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode JSON log entry: %v", err)
	}
	if entry["msg"] != "ready" || entry["source"] == nil {
		t.Fatalf("JSON log does not contain message/source: %#v", entry)
	}
}

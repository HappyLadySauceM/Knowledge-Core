package trace_test

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"testing"
	"time"

	tracepkg "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestRuntimeCreatesTraceIDWithoutExporter(t *testing.T) {
	runtime, err := tracepkg.New(context.Background(), tracepkg.Config{
		Service: "identity", Environment: "test", SampleRatio: 1,
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	ctx, span := runtime.Tracer("test").Start(context.Background(), "root")
	defer span.End()
	if traceID := tracepkg.TraceID(ctx); traceID == "" {
		t.Fatal("runtime without exporter created an invalid trace ID")
	}
	if spanID := tracepkg.SpanID(ctx); spanID == "" {
		t.Fatal("runtime without exporter created an invalid span ID")
	}
}

func TestRuntimeUsesParentBasedSampler(t *testing.T) {
	runtime, err := tracepkg.New(context.Background(), tracepkg.Config{
		Service: "identity", Environment: "test", SampleRatio: 0,
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Shutdown(context.Background()) })

	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx, child := runtime.Tracer("test").Start(trace.ContextWithRemoteSpanContext(context.Background(), parent), "child")
	defer child.End()
	if !trace.SpanContextFromContext(ctx).IsSampled() {
		t.Fatal("sampled remote parent was not honored at a zero root sample ratio")
	}
}

func TestConfigValidation(t *testing.T) {
	for _, config := range []tracepkg.Config{
		{Environment: "test", SampleRatio: 1},
		{Service: "identity", SampleRatio: 1},
		{Service: "identity", Environment: "test", SampleRatio: -0.1},
		{Service: "identity", Environment: "test", SampleRatio: 1.1},
		{Service: "identity", Environment: "test", SampleRatio: 1, Endpoint: "http://collector:4317"},
		{Service: "identity", Environment: "test", SampleRatio: 1, Endpoint: "https://collector:4317", Insecure: true},
		{Service: "identity", Environment: "test", SampleRatio: 1, BatchTimeout: -time.Second},
		{Service: "identity", Environment: "test", SampleRatio: 1, ExportTimeout: -time.Second},
		{Service: "identity", Environment: "test", SampleRatio: 1, Insecure: true, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}},
		{Service: "identity", Environment: "test", SampleRatio: 1, Headers: map[string]string{"bad key": "value"}},
		{Service: "identity", Environment: "test", SampleRatio: 1, Headers: map[string]string{"authorization": "line\nbreak"}},
		{Service: "identity", Environment: "test", SampleRatio: 1, Headers: map[string]string{"Authorization": "one", "authorization": "two"}},
	} {
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", config)
		}
	}
}

func TestConfigAcceptsExporterControls(t *testing.T) {
	config := tracepkg.Config{
		Service:       "identity",
		Environment:   "test",
		Endpoint:      "collector:4317",
		SampleRatio:   0.25,
		Headers:       map[string]string{"authorization": "Bearer secret"},
		BatchTimeout:  2 * time.Second,
		ExportTimeout: 3 * time.Second,
		TLSConfig:     &tls.Config{MinVersion: tls.VersionTLS13},
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

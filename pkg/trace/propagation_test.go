package trace

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestHertzMiddlewareExtractsTraceAndCreatesRequestID(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	runtime := &Runtime{provider: provider}
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(Propagator())
	t.Cleanup(func() {
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	var handlerSpan oteltrace.SpanContext
	var handlerRequestID string
	engine := server.New()
	engine.Use(HertzServerMiddleware(runtime, nil))
	engine.GET("/documents/:id", func(ctx context.Context, request *app.RequestContext) {
		handlerSpan = oteltrace.SpanContextFromContext(ctx)
		handlerRequestID = metadata.RequestID(ctx)
		request.String(200, "ok")
	})

	traceID := "0123456789abcdef0123456789abcdef"
	parentSpanID := "0123456789abcdef"
	response := ut.PerformRequest(
		engine.Engine,
		"GET",
		"/documents/42?token=must-not-appear",
		nil,
		ut.Header{Key: "traceparent", Value: "00-" + traceID + "-" + parentSpanID + "-01"},
	)
	if response.Code != 200 || handlerSpan.TraceID().String() != traceID {
		t.Fatalf("response = %d, handler span = %s", response.Code, handlerSpan.TraceID())
	}
	if handlerRequestID == "" || string(response.Header().Peek(RequestIDHeader)) != handlerRequestID {
		t.Fatalf("request IDs = handler %q, response %q", handlerRequestID, response.Header().Peek(RequestIDHeader))
	}
	if got := string(response.Header().Peek(TraceIDHeader)); got != traceID {
		t.Fatalf("trace response header = %q, want %q", got, traceID)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "GET /documents/:id" || span.Parent().SpanID().String() != parentSpanID || !span.Parent().IsRemote() {
		t.Fatalf("span = name %q, parent %s, remote %t", span.Name(), span.Parent().SpanID(), span.Parent().IsRemote())
	}
	for _, item := range span.Attributes() {
		if strings.Contains(string(item.Key), "url") || strings.Contains(item.Value.String(), "must-not-appear") {
			t.Fatalf("span attribute leaked URL data: %s=%q", item.Key, item.Value.String())
		}
	}
}

func TestKitexMiddlewaresPropagateTraceAndRequestID(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	runtime := &Runtime{provider: provider}
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(Propagator())
	t.Cleanup(func() {
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	rootCtx, root := runtime.Tracer("test").Start(context.Background(), "root")
	rootCtx = metadata.WithRequestID(rootCtx, "request-1")
	var clientSpan, serverSpan oteltrace.SpanContext
	client := KitexClientMiddleware(runtime)(func(clientCtx context.Context, _, _ any) error {
		clientSpan = oteltrace.SpanContextFromContext(clientCtx)
		remoteCtx := context.Background()
		for _, key := range append([]string{RequestIDMetadataKey}, Propagator().Fields()...) {
			if value, exists := metainfo.GetPersistentValue(clientCtx, key); exists {
				remoteCtx = metainfo.WithPersistentValue(remoteCtx, key, value)
			}
		}
		server := KitexServerMiddleware(runtime)(func(serverCtx context.Context, _, _ any) error {
			serverSpan = oteltrace.SpanContextFromContext(serverCtx)
			if metadata.RequestID(serverCtx) != "request-1" {
				t.Fatalf("server request ID = %q", metadata.RequestID(serverCtx))
			}
			return nil
		})
		return server(remoteCtx, nil, nil)
	})
	if err := client(rootCtx, nil, nil); err != nil {
		t.Fatalf("client middleware error = %v", err)
	}
	root.End()

	if clientSpan.TraceID() != root.SpanContext().TraceID() || serverSpan.TraceID() != root.SpanContext().TraceID() {
		t.Fatalf("trace IDs = root %s, client %s, server %s", root.SpanContext().TraceID(), clientSpan.TraceID(), serverSpan.TraceID())
	}
	if clientSpan.SpanID() == serverSpan.SpanID() {
		t.Fatalf("client and server span IDs are equal: %s", clientSpan.SpanID())
	}
}

func TestRuntimeAcceptsNilLogger(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	runtime, err := New(context.Background(), Config{Service: "identity", Environment: "test", SampleRatio: 1}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

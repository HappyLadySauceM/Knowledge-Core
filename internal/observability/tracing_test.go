package observability

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestHertzServerMiddlewareExtractsTraceContextWithoutRecordingURL(t *testing.T) {
	runtime, recorder := newRecordedRuntime(t)
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })

	var handlerSpan trace.SpanContext
	engine := server.New()
	engine.Use(
		func(ctx context.Context, request *app.RequestContext) {
			request.Next(WithRequestID(ctx, "request-1"))
		},
		HertzServerMiddleware(runtime, nil),
	)
	engine.GET("/documents/:id", func(ctx context.Context, request *app.RequestContext) {
		handlerSpan = trace.SpanContextFromContext(ctx)
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
	if responseTraceID := string(response.Header().Peek("X-Trace-ID")); responseTraceID != traceID {
		t.Fatalf("X-Trace-ID = %q, want %q", responseTraceID, traceID)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Name() != "GET /documents/:id" || span.Parent().SpanID().String() != parentSpanID || !span.Parent().IsRemote() {
		t.Fatalf("span = name %q, parent %s, remote %t", span.Name(), span.Parent().SpanID(), span.Parent().IsRemote())
	}
	attributes := make(map[string]string)
	for _, item := range span.Attributes() {
		attributes[string(item.Key)] = item.Value.Emit()
	}
	if attributes["http.route"] != "/documents/:id" || attributes["request.id"] != "request-1" {
		t.Fatalf("span attributes = %#v", attributes)
	}
	for key, value := range attributes {
		if strings.Contains(key, "url") || strings.Contains(value, "must-not-appear") {
			t.Fatalf("span attribute leaked URL data: %s=%q", key, value)
		}
	}
}

func TestKitexMiddlewaresPreserveTraceParentAcrossTransportContext(t *testing.T) {
	runtime, recorder := newRecordedRuntime(t)
	previousPropagator := otel.GetTextMapPropagator()
	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	otel.SetTextMapPropagator(propagator)
	t.Cleanup(func() { otel.SetTextMapPropagator(previousPropagator) })

	rootCtx, root := runtime.Tracer("test").Start(context.Background(), "HTTP GET", trace.WithSpanKind(trace.SpanKindServer))
	rootCtx = WithRequestID(rootCtx, "request-1")
	var clientSpanContext, serverSpanContext trace.SpanContext
	client := KitexClientMiddleware(runtime)(func(clientCtx context.Context, _, _ interface{}) error {
		clientSpanContext = trace.SpanContextFromContext(clientCtx)
		remoteCtx := context.Background()
		for _, key := range append([]string{requestIDMetadataKey}, propagator.Fields()...) {
			if value, exists := metainfo.GetPersistentValue(clientCtx, key); exists {
				remoteCtx = metainfo.WithPersistentValue(remoteCtx, key, value)
			}
		}
		server := KitexServerMiddleware(runtime)(func(serverCtx context.Context, _, _ interface{}) error {
			serverSpanContext = trace.SpanContextFromContext(serverCtx)
			if RequestID(serverCtx) != "request-1" {
				t.Fatalf("server request ID = %q", RequestID(serverCtx))
			}
			return nil
		})
		return server(remoteCtx, nil, nil)
	})
	if err := client(rootCtx, nil, nil); err != nil {
		t.Fatalf("client middleware error = %v", err)
	}
	root.End()

	if clientSpanContext.TraceID() != root.SpanContext().TraceID() || serverSpanContext.TraceID() != root.SpanContext().TraceID() {
		t.Fatalf("trace IDs = root %s, client %s, server %s", root.SpanContext().TraceID(), clientSpanContext.TraceID(), serverSpanContext.TraceID())
	}
	if clientSpanContext.SpanID() == serverSpanContext.SpanID() {
		t.Fatalf("client and server span IDs are equal: %s", clientSpanContext.SpanID())
	}
	var serverParent trace.SpanContext
	for _, span := range recorder.Ended() {
		if span.SpanContext().SpanID() == serverSpanContext.SpanID() {
			serverParent = span.Parent()
		}
	}
	if serverParent.SpanID() != clientSpanContext.SpanID() || !serverParent.IsRemote() {
		t.Fatalf("server parent = %s (remote %t), want client %s", serverParent.SpanID(), serverParent.IsRemote(), clientSpanContext.SpanID())
	}
}

func TestRuntimeCreatesTraceIDWithoutExporter(t *testing.T) {
	runtime, err := New(context.Background(), Config{
		Service: "gateway", Environment: "test", Level: "info", Output: io.Discard, SampleRatio: 1,
	})
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
	if traceID := TraceID(ctx); traceID == "" {
		t.Fatal("runtime without exporter created an invalid trace ID")
	}
}

func newRecordedRuntime(t *testing.T) (*Runtime, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})
	return &Runtime{
		logger:         slog.New(slog.NewJSONHandler(io.Discard, nil)),
		tracerProvider: provider,
		sdkProvider:    provider,
	}, recorder
}

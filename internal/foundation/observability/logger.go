package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type Config struct {
	Service      string
	Environment  string
	Level        string
	Output       io.Writer
	OTLPEndpoint string
	SampleRatio  float64
}

type Runtime struct {
	logger         *slog.Logger
	level          *slog.LevelVar
	tracerProvider trace.TracerProvider
	sdkProvider    *sdktrace.TracerProvider
}

func New(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	level, _ := parseLevel(cfg.Level)
	levelVar := &slog.LevelVar{}
	levelVar.Set(level)
	output := cfg.Output
	if output == nil {
		output = os.Stderr
	}
	jsonHandler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level:       levelVar,
		ReplaceAttr: redactAttribute,
	})
	logger := slog.New(&contextHandler{Handler: jsonHandler}).With(
		slog.String("service", cfg.Service),
		slog.String("environment", cfg.Environment),
	)

	tracerProvider, sdkProvider, err := newTracerProvider(ctx, cfg)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		logger:         logger,
		level:          levelVar,
		tracerProvider: tracerProvider,
		sdkProvider:    sdkProvider,
	}
	slog.SetDefault(logger)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Error("OpenTelemetry error", "component", "otel", "error", err)
	}))
	return runtime, nil
}

func (c Config) Validate() error {
	switch {
	case c.Service == "":
		return errors.New("validate observability config: service is required")
	case c.Environment == "":
		return errors.New("validate observability config: environment is required")
	case c.SampleRatio < 0 || c.SampleRatio > 1:
		return errors.New("validate observability config: trace sample ratio must be between 0 and 1")
	}
	if _, err := parseLevel(c.Level); err != nil {
		return err
	}
	if c.OTLPEndpoint != "" {
		endpoint, err := url.ParseRequestURI(c.OTLPEndpoint)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			return errors.New("validate observability config: OTLP endpoint must be an absolute http or https URL")
		}
	}
	return nil
}

func (r *Runtime) Logger() *slog.Logger {
	if r == nil || r.logger == nil {
		return slog.Default()
	}
	return r.logger
}

func (r *Runtime) Tracer(name string) trace.Tracer {
	if r == nil || r.tracerProvider == nil {
		return noop.NewTracerProvider().Tracer(name)
	}
	return r.tracerProvider.Tracer(name)
}

func (r *Runtime) InstallCloudWeGo() {
	if r != nil {
		installCloudWeGoLoggers(r)
	}
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil || r.sdkProvider == nil {
		return nil
	}
	return errors.Join(r.sdkProvider.ForceFlush(ctx), r.sdkProvider.Shutdown(ctx))
}

func NewBootstrapLogger(output io.Writer, service string) *slog.Logger {
	if output == nil {
		output = os.Stderr
	}
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{ReplaceAttr: redactAttribute})).With(
		"service", service,
		"environment", "bootstrap",
	)
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("validate observability config: unsupported log level %q", value)
	}
}

func newTracerProvider(ctx context.Context, cfg Config) (trace.TracerProvider, *sdktrace.TracerProvider, error) {
	if cfg.OTLPEndpoint == "" {
		return noop.NewTracerProvider(), nil, nil
	}
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(cfg.OTLPEndpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.Service),
		semconv.DeploymentEnvironmentName(cfg.Environment),
		attribute.String("service.namespace", "knowledge-core"),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("create OpenTelemetry resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	return provider, provider, nil
}

type contextHandler struct {
	slog.Handler
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	existing := make(map[string]struct{})
	record.Attrs(func(attr slog.Attr) bool {
		existing[attr.Key] = struct{}{}
		return true
	})
	for _, attr := range contextAttributes(ctx) {
		if _, exists := existing[attr.Key]; !exists {
			record.AddAttrs(attr)
		}
	}
	return h.Handler.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name)}
}

func contextAttributes(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	attrs := make([]slog.Attr, 0, 4)
	if requestID := RequestID(ctx); requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		attrs = append(attrs,
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	if userID := UserID(ctx); userID > 0 {
		attrs = append(attrs, slog.Int64("user_id", userID))
	}
	return attrs
}

func redactAttribute(_ []string, attr slog.Attr) slog.Attr {
	if isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, "[REDACTED]")
	}
	return attr
}

func isSensitiveKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(key))
	switch normalized {
	case "password", "password_hash", "token", "access_token", "refresh_token", "authorization",
		"cookie", "set_cookie", "dsn", "api_key", "private_key", "secret", "payload", "content",
		"body", "request_body", "response_body":
		return true
	}
	for _, suffix := range []string{"_password", "_secret", "_dsn", "_api_key", "_private_key", "_authorization"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

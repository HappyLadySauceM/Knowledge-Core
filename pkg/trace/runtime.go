// Package trace owns OpenTelemetry tracing and CloudWeGo propagation hooks.
package trace

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"
)

type Config struct {
	Service       string
	Environment   string
	Endpoint      string
	SampleRatio   float64
	Insecure      bool
	Headers       map[string]string
	BatchTimeout  time.Duration
	ExportTimeout time.Duration
	TLSConfig     *tls.Config
}

type Runtime struct {
	provider     *sdktrace.TracerProvider
	shutdownOnce sync.Once
	shutdownErr  error
}

type suppressionContextKey struct{}

// Suppress marks a request as low-value telemetry. The marker is carried with
// the request context so all instrumented dependencies can drop their spans.
func Suppress(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, suppressionContextKey{}, true)
}

func IsSuppressed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	suppressed, _ := ctx.Value(suppressionContextKey{}).(bool)
	return suppressed
}

// IgnoreHTTPPath contains endpoints that are polled continuously and should
// be observed through health/metric signals rather than traces.
func IgnoreHTTPPath(path string) bool {
	switch strings.TrimSpace(path) {
	case "/metrics", "/livez", "/readyz", "/health/live", "/health/ready":
		return true
	default:
		return false
	}
}

func IgnoreRPCMethod(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "ping", "live", "health", "healthcheck":
		return true
	default:
		return false
	}
}

type suppressionSpanProcessor struct {
	delegate sdktrace.SpanProcessor
	skipped  sync.Map
}

func (p *suppressionSpanProcessor) OnStart(parent context.Context, span sdktrace.ReadWriteSpan) {
	if IsSuppressed(parent) {
		p.skipped.Store(span.SpanContext().SpanID().String(), struct{}{})
		return
	}
	p.delegate.OnStart(parent, span)
}

func (p *suppressionSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	key := span.SpanContext().SpanID().String()
	if _, skipped := p.skipped.LoadAndDelete(key); skipped {
		return
	}
	p.delegate.OnEnd(span)
}

func (p *suppressionSpanProcessor) Shutdown(ctx context.Context) error {
	return p.delegate.Shutdown(ctx)
}
func (p *suppressionSpanProcessor) ForceFlush(ctx context.Context) error {
	return p.delegate.ForceFlush(ctx)
}

func New(ctx context.Context, config Config, logger *slog.Logger) (*Runtime, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}

	serviceResource, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", strings.TrimSpace(config.Service)),
		attribute.String("deployment.environment.name", strings.TrimSpace(config.Environment)),
		attribute.String("service.namespace", "knowledge-core"),
	))
	if err != nil {
		return nil, fmt.Errorf("create tracing resource: %w", err)
	}
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(serviceResource),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
	}
	if config.Endpoint != "" {
		exporter, err := otlptracegrpc.New(ctx, exporterOptions(config)...)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		batchOptions := make([]sdktrace.BatchSpanProcessorOption, 0, 2)
		if config.BatchTimeout > 0 {
			batchOptions = append(batchOptions, sdktrace.WithBatchTimeout(config.BatchTimeout))
		}
		if config.ExportTimeout > 0 {
			batchOptions = append(batchOptions, sdktrace.WithExportTimeout(config.ExportTimeout))
		}
		processor := sdktrace.NewBatchSpanProcessor(exporter, batchOptions...)
		options = append(options, sdktrace.WithSpanProcessor(&suppressionSpanProcessor{delegate: processor}))
	}

	provider := sdktrace.NewTracerProvider(options...)
	runtime := &Runtime{provider: provider}
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(Propagator())
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Error("OpenTelemetry error", "component", "otel", "error", err)
	}))
	return runtime, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Service) == "" {
		return errors.New("validate tracing config: service is required")
	}
	if strings.TrimSpace(c.Environment) == "" {
		return errors.New("validate tracing config: environment is required")
	}
	if c.SampleRatio < 0 || c.SampleRatio > 1 {
		return errors.New("validate tracing config: sample ratio must be between 0 and 1")
	}
	if c.BatchTimeout < 0 {
		return errors.New("validate tracing config: batch timeout cannot be negative")
	}
	if c.ExportTimeout < 0 {
		return errors.New("validate tracing config: export timeout cannot be negative")
	}
	if c.Insecure && c.TLSConfig != nil {
		return errors.New("validate tracing config: insecure and TLS config are mutually exclusive")
	}
	normalizedHeaders := make(map[string]struct{}, len(c.Headers))
	for key, value := range c.Headers {
		if !validHeaderKey(key) || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("validate tracing config: invalid OTLP header %q", key)
		}
		normalized := strings.ToLower(key)
		if _, exists := normalizedHeaders[normalized]; exists {
			return fmt.Errorf("validate tracing config: duplicate OTLP header %q", normalized)
		}
		normalizedHeaders[normalized] = struct{}{}
	}
	if c.Endpoint == "" {
		return nil
	}
	if strings.TrimSpace(c.Endpoint) != c.Endpoint || strings.ContainsAny(c.Endpoint, "\r\n\t ") {
		return errors.New("validate tracing config: endpoint contains whitespace")
	}
	if strings.Contains(c.Endpoint, "://") {
		endpoint, err := url.ParseRequestURI(c.Endpoint)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
			return errors.New("validate tracing config: endpoint must be an HTTP(S) URL or host:port")
		}
		if endpoint.Scheme == "http" && !c.Insecure {
			return errors.New("validate tracing config: HTTP endpoint requires insecure=true")
		}
		if endpoint.Scheme == "https" && c.Insecure {
			return errors.New("validate tracing config: HTTPS endpoint is incompatible with insecure=true")
		}
	}
	return nil
}

func Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func (r *Runtime) Tracer(name string) oteltrace.Tracer {
	if r == nil || r.provider == nil {
		return noop.NewTracerProvider().Tracer(name)
	}
	return r.provider.Tracer(name)
}

func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil || r.provider == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.shutdownOnce.Do(func() {
		r.shutdownErr = errors.Join(r.provider.ForceFlush(ctx), r.provider.Shutdown(ctx))
	})
	return r.shutdownErr
}

func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanContext := oteltrace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func SpanID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	spanContext := oteltrace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.SpanID().String()
}

func exporterOptions(config Config) []otlptracegrpc.Option {
	options := make([]otlptracegrpc.Option, 0, 4)
	if strings.Contains(config.Endpoint, "://") {
		options = append(options, otlptracegrpc.WithEndpointURL(config.Endpoint))
	} else {
		options = append(options, otlptracegrpc.WithEndpoint(config.Endpoint))
	}
	if config.Insecure {
		options = append(options, otlptracegrpc.WithInsecure())
	}
	if len(config.Headers) > 0 {
		headers := make(map[string]string, len(config.Headers))
		for key, value := range config.Headers {
			headers[strings.ToLower(key)] = value
		}
		options = append(options, otlptracegrpc.WithHeaders(headers))
	}
	if config.TLSConfig != nil {
		options = append(options, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(config.TLSConfig.Clone())))
	}
	return options
}

func validHeaderKey(key string) bool {
	if key == "" {
		return false
	}
	for _, current := range key {
		if (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z') || (current >= '0' && current <= '9') || current == '-' || current == '_' || current == '.' {
			continue
		}
		return false
	}
	return true
}

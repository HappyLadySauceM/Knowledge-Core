package redis

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	coremetrics "github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	"github.com/redis/go-redis/extra/redisotel/v9"
	redisclient "github.com/redis/go-redis/v9"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestOpenRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	opts := *option.NewRedisOptions()
	opts.Address = ""
	if _, err := Open(context.Background(), opts, "cache", newMetricsRegistry(t), nil); err == nil || !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("Open() error = %v, want invalid options", err)
	}
}

func TestOpenClosesClientAfterPingFailure(t *testing.T) {
	t.Parallel()
	opts := *option.NewRedisOptions()
	opts.Address = "127.0.0.1:1"
	opts.DialTimeout = 25 * time.Millisecond
	opts.ReadTimeout = 25 * time.Millisecond
	opts.WriteTimeout = 25 * time.Millisecond
	opts.MaxRetries = -1
	registry := newMetricsRegistry(t)
	if _, err := Open(context.Background(), opts, "cache", registry, nil); err == nil || !strings.Contains(err.Error(), "ping redis") {
		t.Fatalf("Open() error = %v, want ping redis failure", err)
	}
	assertMetricAbsent(t, registry, "knowledge_core_redis_pool_connections")
}

func TestInstrumentTracingFiltersDialSpans(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go acceptAndClose(listener)

	client := redisclient.NewClient(&redisclient.Options{
		Addr:         listener.Addr().String(),
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1,
		PoolSize:     1,
		MinIdleConns: 0,
	})
	t.Cleanup(func() { _ = client.Close() })
	if err := instrumentClientTracing(client, redisotel.WithTracerProvider(provider)); err != nil {
		t.Fatalf("instrumentClientTracing() error = %v", err)
	}

	// Force a dial against the local listener; Redis protocol may fail after connect.
	// 强制对本地 listener 拨号；协议层失败不影响 dial span 断言。
	_ = client.Ping(context.Background()).Err()

	for _, span := range recorder.Ended() {
		if span.Name() == "redis.dial" {
			t.Fatalf("unexpected redis.dial span exported")
		}
	}
}

func acceptAndClose(listener net.Listener) {
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		_ = connection.Close()
	}
}

func TestPoolMetricsAreUnregisteredOnClose(t *testing.T) {
	registry := newMetricsRegistry(t)
	client := redisclient.NewClient(&redisclient.Options{
		Addr:         "127.0.0.1:1",
		PoolSize:     7,
		MinIdleConns: 2,
	})
	collector := newPoolCollector("cache", client)
	if err := registry.Register(collector); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	resource := &Resource{
		Client:           client,
		metricsRegistry:  registry,
		metricsCollector: collector,
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	foundMaximum := false
	for _, family := range families {
		if family.GetName() != "knowledge_core_redis_pool_max_connections" {
			continue
		}
		foundMaximum = family.Metric[0].GetGauge().GetValue() == 7
	}
	if !foundMaximum {
		t.Fatal("Redis pool maximum metric was not exported")
	}

	if err := resource.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	assertMetricAbsent(t, registry, "knowledge_core_redis_pool_connections")
}

func TestNilResourceLifecycleIsSafe(t *testing.T) {
	t.Parallel()
	var resource *Resource
	if err := resource.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := resource.Ping(context.Background()); err == nil {
		t.Fatal("Ping() error = nil, want nil client error")
	}
}

func TestPingRejectsNilContext(t *testing.T) {
	t.Parallel()
	resource := &Resource{Client: redisclient.NewClient(&redisclient.Options{
		Addr: "127.0.0.1:1",
	})}
	t.Cleanup(func() { _ = resource.Close() })

	if err := resource.Ping(nil); err == nil || !strings.Contains(err.Error(), "context is required") { //nolint:staticcheck // Explicitly verifies the nil-context contract.
		t.Fatalf("Ping() error = %v, want context is required", err)
	}
}

func newMetricsRegistry(t *testing.T) *coremetrics.Registry {
	t.Helper()
	registry, err := coremetrics.NewRegistry(coremetrics.Config{
		Service:     "redis-test",
		Environment: "testing",
		Version:     "test",
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func assertMetricAbsent(t *testing.T, registry *coremetrics.Registry, name string) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			t.Fatalf("metric %q is still registered", name)
		}
	}
}

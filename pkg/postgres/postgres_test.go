package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	logpkg "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGORMLoggerPreservesTraceContextAndFiltersVariables(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger, _, err := logpkg.New("identity", "test", "debug", &output)
	if err != nil {
		t.Fatalf("log.New() error = %v", err)
	}
	opts := *option.NewPostgreSQLOptions()
	databaseLogger := newGORMLogger(logger, opts)
	filter, ok := databaseLogger.(gorm.ParamsFilter)
	if !ok {
		t.Fatal("GORM logger does not implement ParamsFilter")
	}
	_, params := filter.ParamsFilter(context.Background(), "SELECT $1", "must-not-appear")
	if len(params) != 0 {
		t.Fatalf("filtered params = %#v, want none", params)
	}

	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	databaseLogger.Trace(ctx, time.Now().Add(-time.Second), func() (string, int64) {
		return "SELECT $1", 1
	}, nil)
	if !bytes.Contains(output.Bytes(), []byte(traceID.String())) {
		t.Fatalf("GORM log is missing trace ID: %s", output.String())
	}
	if bytes.Contains(output.Bytes(), []byte("must-not-appear")) {
		t.Fatalf("GORM log leaked query variables: %s", output.String())
	}
}

func TestConnectionStringEscapesCredentialsAndConfiguresTLS(t *testing.T) {
	t.Parallel()
	opts := *option.NewPostgreSQLOptions()
	opts.User = "user@example"
	opts.Password = "p@ss:/word"
	opts.Database = "identity"
	opts.SSLMode = "verify-full"
	opts.TLS = option.TLSOptions{Enabled: true, CAFile: "ca.pem", CertFile: "client.pem", KeyFile: "client-key.pem"}

	dsn, err := connectionString(opts)
	if err != nil {
		t.Fatalf("connectionString() error = %v", err)
	}
	for _, expected := range []string{"user%40example:p%40ss%3A%2Fword", "sslmode=verify-full", "sslrootcert=ca.pem"} {
		if !strings.Contains(dsn, expected) {
			t.Errorf("connectionString() = %q, want substring %q", dsn, expected)
		}
	}
}

func TestInstallTracingRegistersExactlyOnce(t *testing.T) {
	t.Parallel()
	pool, err := sql.Open("pgx", "host=127.0.0.1 port=1")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer func() {
		if closeErr := pool.Close(); closeErr != nil {
			t.Errorf("pool.Close() error = %v", closeErr)
		}
	}()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{
		DisableAutomaticPing: true,
	})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := installTracing(db, "identity"); err != nil {
		t.Fatalf("installTracing() error = %v", err)
	}
	if _, exists := db.Plugins["otelgorm"]; !exists {
		t.Fatal("OpenTelemetry plugin was not registered")
	}
	if err := installTracing(db, "identity"); err == nil {
		t.Fatal("second installTracing() error = nil, want duplicate registration error")
	}
}

func TestOpenRejectsInvalidOptionsBeforeConnecting(t *testing.T) {
	t.Parallel()
	opts := *option.NewPostgreSQLOptions()
	opts.Host = ""
	opts.ConnectTimeout = 10 * time.Millisecond

	_, err := Open(context.Background(), opts, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("Open() error = %v, want invalid options", err)
	}
}

func TestOpenConnectionFailureIncludesPingContext(t *testing.T) {
	t.Parallel()
	opts := *option.NewPostgreSQLOptions()
	opts.Host = "127.0.0.1"
	opts.Port = 1
	opts.ConnectTimeout = 50 * time.Millisecond

	_, err := Open(context.Background(), opts, nil)
	if err == nil {
		t.Fatal("Open() error = nil, want connection failure")
	}
	if !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("Open() error = %v, want postgres context", err)
	}
}

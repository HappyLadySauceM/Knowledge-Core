// Package postgres owns the lifecycle of the shared GORM PostgreSQL handle.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	semconv "go.opentelemetry.io/otel/semconv/v1.30.0"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/opentelemetry/tracing"
)

// Open validates options, opens a GORM PostgreSQL handle, configures its
// database/sql pool, and verifies the connection before returning it.
func Open(ctx context.Context, opts option.PostgreSQLOptions, logger *slog.Logger) (*gorm.DB, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("open postgres: invalid options: %w", err)
	}
	if ctx == nil {
		return nil, errors.New("open postgres: context is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if opts.TLS.Enabled {
		if _, err := opts.TLS.ClientTLSConfig(); err != nil {
			return nil, fmt.Errorf("open postgres: invalid TLS configuration: %w", err)
		}
	}

	dsn, err := connectionString(opts)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	gormLog := newGORMLogger(logger, opts)
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn}), &gorm.Config{
		Logger:               gormLog,
		NowFunc:              func() time.Time { return time.Now().UTC() },
		PrepareStmt:          opts.PrepareStmt,
		TranslateError:       false, // Keep *pgconn.PgError available to errors.As.
		DisableAutomaticPing: true,  // PingContext below is bounded by the startup context.
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get postgres connection pool: %w", err)
	}
	sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(opts.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(opts.ConnMaxIdleTime)
	pingCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := installTracing(db, opts.Database); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("install postgres OpenTelemetry plugin: %w", err)
	}
	return db, nil
}

func newGORMLogger(logger *slog.Logger, opts option.PostgreSQLOptions) gormlogger.Interface {
	return gormlogger.NewSlogLogger(logger.With(slog.String("component", "gorm")), gormlogger.Config{
		SlowThreshold:             opts.SlowThreshold,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  false,
	})
}

func installTracing(db *gorm.DB, databaseName string) error {
	return db.Use(tracing.NewPlugin(
		tracing.WithDBSystem("postgresql"),
		tracing.WithAttributes(semconv.DBNamespace(databaseName)),
		tracing.WithoutQueryVariables(),
		tracing.WithoutMetrics(), // Pool metrics are exported by pkg/metrics.
	))
}

// Ping verifies a previously opened GORM handle using the caller's deadline.
func Ping(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return errors.New("ping postgres: database is nil")
	}
	if ctx == nil {
		return errors.New("ping postgres: context is required")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get postgres connection pool: %w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	return nil
}

// Close closes the database/sql pool owned by a GORM handle.
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get postgres connection pool: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("close postgres: %w", err)
	}
	return nil
}

func connectionString(opts option.PostgreSQLOptions) (string, error) {
	if opts.DSN != "" {
		return opts.DSN, nil
	}
	if opts.TLS.ServerName != "" && opts.TLS.ServerName != opts.Host {
		return "", fmt.Errorf("TLS server_name %q must match postgres host %q", opts.TLS.ServerName, opts.Host)
	}
	if opts.TLS.InsecureSkipVerify && opts.SSLMode != "require" {
		return "", fmt.Errorf("TLS insecure_skip_verify requires postgres ssl_mode=require")
	}

	credentials := url.User(opts.User)
	if opts.Password != "" {
		credentials = url.UserPassword(opts.User, opts.Password)
	}
	value := &url.URL{
		Scheme: "postgres",
		User:   credentials,
		Host:   net.JoinHostPort(opts.Host, strconv.Itoa(opts.Port)),
		Path:   opts.Database,
	}
	query := value.Query()
	query.Set("sslmode", opts.SSLMode)
	query.Set("TimeZone", opts.TimeZone)
	query.Set("connect_timeout", strconv.FormatInt(max(1, int64(opts.ConnectTimeout/time.Second)), 10))
	if opts.TLS.Enabled {
		if opts.TLS.CAFile != "" {
			query.Set("sslrootcert", opts.TLS.CAFile)
		}
		if opts.TLS.CertFile != "" {
			query.Set("sslcert", opts.TLS.CertFile)
			query.Set("sslkey", opts.TLS.KeyFile)
		}
	}
	value.RawQuery = query.Encode()
	return value.String(), nil
}

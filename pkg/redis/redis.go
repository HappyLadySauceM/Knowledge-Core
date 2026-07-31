// Package redis owns the lifecycle of a traced go-redis client.
package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	"github.com/redis/go-redis/extra/redisotel/v9"
	redisclient "github.com/redis/go-redis/v9"
)

// Resource owns an instrumented go-redis client. Client is exposed for direct
// Redis operations; Resource retains ownership of its lifecycle.
type Resource struct {
	Client      *redisclient.Client
	metricsDone chan struct{}
	closeOnce   sync.Once
	closeErr    error
}

func Open(ctx context.Context, opts option.RedisOptions, logger *slog.Logger) (*Resource, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("open redis: invalid options: %w", err)
	}
	if ctx == nil {
		return nil, errors.New("open redis: context is required")
	}
	tlsConfig, err := opts.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("open redis: invalid TLS configuration: %w", err)
	}
	client := redisclient.NewClient(&redisclient.Options{
		Addr:                  opts.Address,
		Username:              opts.Username,
		Password:              opts.Password,
		DB:                    opts.DB,
		DialTimeout:           opts.DialTimeout,
		ReadTimeout:           opts.ReadTimeout,
		WriteTimeout:          opts.WriteTimeout,
		ContextTimeoutEnabled: true,
		PoolSize:              opts.PoolSize,
		MinIdleConns:          opts.MinIdleConns,
		MaxRetries:            opts.MaxRetries,
		TLSConfig:             tlsConfig,
	})
	metricsDone := make(chan struct{})
	if err := redisotel.InstrumentTracing(client, redisotel.WithDBStatement(false)); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("instrument redis tracing: %w", err)
	}
	if err := redisotel.InstrumentMetrics(client, redisotel.WithCloseChan(metricsDone)); err != nil {
		close(metricsDone)
		_ = client.Close()
		return nil, fmt.Errorf("instrument redis metrics: %w", err)
	}
	resource := &Resource{Client: client, metricsDone: metricsDone}
	if err := resource.Ping(ctx); err != nil {
		_ = resource.Close()
		return nil, err
	}
	if logger != nil {
		logger.DebugContext(ctx, "redis connected", slog.String("address", opts.Address), slog.Int("db", opts.DB))
	}
	return resource, nil
}

func (r *Resource) Ping(ctx context.Context) error {
	if r == nil || r.Client == nil {
		return errors.New("ping redis: client is nil")
	}
	if ctx == nil {
		return errors.New("ping redis: context is required")
	}
	if err := r.Client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}

func (r *Resource) Close() error {
	if r == nil || r.Client == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.metricsDone != nil {
			close(r.metricsDone)
		}
		if err := r.Client.Close(); err != nil {
			r.closeErr = fmt.Errorf("close redis: %w", err)
		}
	})
	return r.closeErr
}

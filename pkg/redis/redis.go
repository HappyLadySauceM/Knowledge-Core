// Package redis owns the lifecycle of a traced go-redis client.
package redis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/extra/redisotel/v9"
	redisclient "github.com/redis/go-redis/v9"
)

// Resource owns an instrumented go-redis client. Client is exposed for direct
// Redis operations; Resource retains ownership of its lifecycle.
type Resource struct {
	Client           *redisclient.Client
	metricsRegistry  *metrics.Registry
	metricsCollector prometheus.Collector
	closeOnce        sync.Once
	closeErr         error
}

func Open(
	ctx context.Context,
	opts option.RedisOptions,
	dependency string,
	metricsRegistry *metrics.Registry,
	logger *slog.Logger,
) (*Resource, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("open redis: invalid options: %w", err)
	}
	if ctx == nil {
		return nil, errors.New("open redis: context is required")
	}
	dependency = strings.TrimSpace(dependency)
	if dependency == "" || metricsRegistry == nil {
		return nil, errors.New("open redis: dependency name and metrics registry are required")
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
	if err := instrumentClientTracing(client); err != nil {
		_ = client.Close()
		return nil, err
	}
	metricsCollector := newPoolCollector(dependency, client)
	if err := metricsRegistry.Register(metricsCollector); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("register redis metrics: %w", err)
	}
	resource := &Resource{
		Client:           client,
		metricsRegistry:  metricsRegistry,
		metricsCollector: metricsCollector,
	}
	if err := resource.Ping(ctx); err != nil {
		_ = resource.Close()
		return nil, err
	}
	if logger != nil {
		logger.DebugContext(ctx, "redis connected", slog.String("address", opts.Address), slog.Int("db", opts.DB))
	}
	return resource, nil
}

func instrumentClientTracing(client *redisclient.Client, extra ...redisotel.TracingOption) error {
	// Drop connection-pool dial spans; they often use a parentless context and become orphan root traces.
	// 过滤连接池拨号 span：无父级 context 时会变成噪音 root trace；保留命令级 Redis span。
	options := []redisotel.TracingOption{
		redisotel.WithDBStatement(false),
		redisotel.WithDialFilter(true),
	}
	options = append(options, extra...)
	if err := redisotel.InstrumentTracing(client, options...); err != nil {
		return fmt.Errorf("instrument redis tracing: %w", err)
	}
	return nil
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
		if r.metricsRegistry != nil && r.metricsCollector != nil {
			r.metricsRegistry.Unregister(r.metricsCollector)
		}
		if err := r.Client.Close(); err != nil {
			r.closeErr = fmt.Errorf("close redis: %w", err)
		}
	})
	return r.closeErr
}

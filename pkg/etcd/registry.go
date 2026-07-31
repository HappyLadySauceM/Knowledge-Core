// Package etcd creates fail-closed Kitex registration and health resources.
package etcd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Resources contains the Kitex registry and the shared etcd client used by
// both registration and health checks. Close owns the client and all
// registration goroutines created from it.
type Resources struct {
	Registry     *LifecycleRegistry
	HealthClient *clientv3.Client

	registry  *etcdRegistry
	endpoints []string
	timeout   time.Duration
	closeOnce sync.Once
	closeErr  error
}

func Open(ctx context.Context, opts option.EtcdOptions, logger *slog.Logger) (*Resources, error) {
	if ctx == nil {
		return nil, errors.New("open etcd registry: context is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("open etcd registry: invalid options: %w", err)
	}
	client, err := newEtcdClient(opts)
	if err != nil {
		return nil, err
	}
	resources, err := newResources(client, opts)
	if err != nil {
		return nil, errors.Join(err, closeEtcdClient(client))
	}
	if err := resources.Ping(ctx); err != nil {
		return nil, errors.Join(err, resources.Close())
	}
	if logger != nil {
		logger.DebugContext(ctx, "etcd registry connected", slog.Int("endpoints", len(opts.Endpoints)), slog.String("prefix", opts.Prefix))
	}
	return resources, nil
}

func newResources(client *clientv3.Client, opts option.EtcdOptions) (*Resources, error) {
	if client == nil {
		return nil, errors.New("create etcd resources: client is nil")
	}
	delegate, err := newEtcdRegistry(client, opts)
	if err != nil {
		return nil, err
	}
	guarded, err := newLifecycleRegistry(delegate)
	if err != nil {
		return nil, fmt.Errorf("create Kitex etcd registry: %w", err)
	}
	return &Resources{
		Registry:     guarded,
		HealthClient: client,
		registry:     delegate,
		endpoints:    append([]string(nil), opts.Endpoints...),
		timeout:      opts.RequestTimeout,
	}, nil
}

func newEtcdClient(opts option.EtcdOptions) (*clientv3.Client, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("create etcd client: invalid options: %w", err)
	}
	tlsConfig, err := opts.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create etcd client: invalid TLS configuration: %w", err)
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   append([]string(nil), opts.Endpoints...),
		DialTimeout: opts.DialTimeout,
		Username:    opts.Username,
		Password:    opts.Password,
		TLS:         tlsConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("create etcd client: %w", err)
	}
	return client, nil
}

func (r *Resources) Ping(ctx context.Context) error {
	if r == nil {
		return errors.New("ping etcd: health client is nil")
	}
	if ctx == nil {
		return errors.New("ping etcd: context is required")
	}
	if err := r.registry.healthError(); err != nil {
		return fmt.Errorf("ping etcd: registry is unhealthy: %w", err)
	}
	if r.HealthClient == nil {
		return errors.New("ping etcd: health client is nil")
	}
	var pingErrors error
	for _, endpoint := range r.endpoints {
		pingCtx, cancel := context.WithTimeout(ctx, r.timeout)
		if _, err := r.HealthClient.Status(pingCtx, endpoint); err == nil {
			cancel()
			if err := r.registry.healthError(); err != nil {
				return fmt.Errorf("ping etcd: registry is unhealthy: %w", err)
			}
			return nil
		} else {
			pingErrors = errors.Join(pingErrors, fmt.Errorf("endpoint %q: %w", endpoint, err))
		}
		cancel()
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("ping etcd: %w", pingErrors)
}

func (r *Resources) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.registry != nil {
			r.registry.close()
		}
		r.closeErr = closeEtcdClient(r.HealthClient)
	})
	return r.closeErr
}

func closeEtcdClient(client *clientv3.Client) error {
	if client == nil {
		return nil
	}
	if err := client.Close(); err != nil {
		return fmt.Errorf("close etcd client: %w", err)
	}
	return nil
}

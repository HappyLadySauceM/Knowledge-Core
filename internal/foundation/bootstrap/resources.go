package bootstrap

import (
	"context"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/cache"
	redisadapter "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/cache/redis"
	foundationconfig "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/config"
	configetcd "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/config/etcd"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database/postgres"
	discoveryetcd "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/discovery/etcd"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/health"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/lifecycle"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/messaging"
	natsadapter "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/messaging/nats"
	"github.com/cloudwego/kitex/pkg/discovery"
	"github.com/cloudwego/kitex/pkg/registry"
)

type Resources struct {
	Database       database.DB
	Cache          cache.KVStore
	DurableBroker  messaging.DurableBroker
	RealtimeBus    messaging.RealtimeBus
	ConfigSource   foundationconfig.Source
	ConfigSnapshot foundationconfig.Snapshot
	Registry       registry.Registry
	Resolver       discovery.Resolver
	Health         *health.Registry

	lifecycle *lifecycle.Manager
}

func Open(ctx context.Context, cfg Config, needs Needs) (_ *Resources, err error) {
	resources := &Resources{Health: health.NewRegistry(), lifecycle: &lifecycle.Manager{}}
	var cleanup []func() error
	defer func() {
		if err != nil {
			for index := len(cleanup) - 1; index >= 0; index-- {
				_ = cleanup[index]()
			}
		}
	}()

	if needs.ConfigSource {
		prefix := fmt.Sprintf("/knowledge-core/%s/config", cfg.Environment)
		source, openErr := configetcd.Open(ctx, configetcd.Config{
			Endpoints:   cfg.Etcd.Endpoints,
			Prefix:      prefix,
			Username:    cfg.Etcd.Username,
			Password:    cfg.Etcd.Password,
			DialTimeout: cfg.Etcd.DialTimeout,
		})
		if openErr != nil {
			return nil, openErr
		}
		resources.ConfigSource = source
		cleanup = append(cleanup, source.Close)
		resources.ConfigSnapshot, err = source.Load(ctx)
		if err != nil {
			return nil, err
		}
		if err = resources.lifecycle.Add(lifecycle.Hook{Name: "etcd-config", Stop: func(context.Context) error { return source.Close() }}); err != nil {
			return nil, err
		}
		_ = resources.Health.Add("etcd-config", func(checkCtx context.Context) error {
			_, checkErr := source.Load(checkCtx)
			return checkErr
		})
	}

	if needs.Database {
		provider := postgres.NewProvider()
		resources.Database, err = provider.Open(ctx, cfg.Database)
		if err != nil {
			return nil, err
		}
		cleanup = append(cleanup, resources.Database.Close)
		if err = resources.lifecycle.Add(lifecycle.Hook{Name: "database", Stop: func(context.Context) error { return resources.Database.Close() }}); err != nil {
			return nil, err
		}
		_ = resources.Health.Add("database", resources.Database.PingContext)
	}

	if needs.Cache {
		resources.Cache, err = redisadapter.Open(ctx, cfg.Redis)
		if err != nil {
			return nil, err
		}
		cleanup = append(cleanup, resources.Cache.Close)
		if err = resources.lifecycle.Add(lifecycle.Hook{Name: "cache", Stop: func(context.Context) error { return resources.Cache.Close() }}); err != nil {
			return nil, err
		}
		_ = resources.Health.Add("cache", resources.Cache.Ping)
	}

	if needs.DurableMessaging {
		resources.DurableBroker, err = natsadapter.OpenDurable(cfg.NATS)
		if err != nil {
			return nil, err
		}
		cleanup = append(cleanup, resources.DurableBroker.Close)
		if err = resources.lifecycle.Add(lifecycle.Hook{Name: "durable-messaging", Stop: func(context.Context) error { return resources.DurableBroker.Close() }}); err != nil {
			return nil, err
		}
		if broker, ok := resources.DurableBroker.(*natsadapter.DurableBroker); ok {
			_ = resources.Health.Add("durable-messaging", broker.Ping)
		}
	}

	if needs.RealtimeMessaging {
		resources.RealtimeBus, err = natsadapter.OpenRealtime(cfg.NATS)
		if err != nil {
			return nil, err
		}
		cleanup = append(cleanup, resources.RealtimeBus.Close)
		if err = resources.lifecycle.Add(lifecycle.Hook{Name: "realtime-messaging", Stop: func(context.Context) error { return resources.RealtimeBus.Close() }}); err != nil {
			return nil, err
		}
		if bus, ok := resources.RealtimeBus.(*natsadapter.RealtimeBus); ok {
			_ = resources.Health.Add("realtime-messaging", bus.Ping)
		}
	}

	if needs.Registry {
		resources.Registry, err = discoveryetcd.NewRegistry(cfg.Etcd)
		if err != nil {
			return nil, err
		}
	}
	if needs.Resolver {
		resources.Resolver, err = discoveryetcd.NewResolver(cfg.Etcd)
		if err != nil {
			return nil, err
		}
	}

	if err := resources.lifecycle.Start(ctx); err != nil {
		return nil, fmt.Errorf("start infrastructure lifecycle: %w", err)
	}
	cleanup = nil
	return resources, nil
}

func (r *Resources) Close(ctx context.Context) error {
	if r == nil || r.lifecycle == nil {
		return nil
	}
	if err := r.lifecycle.Stop(ctx); err != nil {
		return fmt.Errorf("close infrastructure resources: %w", err)
	}
	return nil
}

func (r *Resources) SetServing(serving bool) {
	if r != nil && r.Health != nil {
		r.Health.SetServing(serving)
	}
}

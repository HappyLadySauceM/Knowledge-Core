// Package context assembles Identity's process-scoped dependency graph.
package context

import (
	stdcontext "context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	etcdresource "github.com/HappyLadySauce/Knowledge-Core/pkg/etcd"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/postgres"
	redisresource "github.com/HappyLadySauce/Knowledge-Core/pkg/redis"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/config"
	identitylogic "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/logic"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/migration"
	identityrepository "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	adminhttp "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/http"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/rpc"
	"gorm.io/gorm"
)

type ServiceContext struct {
	Config     config.Config
	Database   *gorm.DB
	Redis      *redisresource.Resource
	Etcd       *etcdresource.Resources
	Register   *identitylogic.RegisterLogic
	RPCHandler *identityrpc.Handler
	RPCServer  *identityrpc.RPCServer
	Admin      *adminhttp.Server
}

func NewServiceContext(ctx stdcontext.Context, cfg config.Config, runtime *coreapp.Runtime) (*ServiceContext, error) {
	if ctx == nil || runtime == nil || runtime.Logger == nil || runtime.Health == nil || runtime.Metrics == nil || runtime.Trace == nil {
		return nil, errors.New("create identity service context: context and initialized runtime are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("create identity service context: %w", err)
	}

	db, err := postgres.Open(ctx, *cfg.PostgreSQL, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("postgres", func(stdcontext.Context) error { return postgres.Close(db) }); err != nil {
		return nil, errors.Join(err, postgres.Close(db))
	}
	if err := migration.AutoMigrate(ctx, db); err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get identity postgres pool for metrics: %w", err)
	}
	if err := runtime.Metrics.RegisterDBStats("identity", sqlDB); err != nil {
		return nil, err
	}

	cache, err := redisresource.Open(ctx, *cfg.Redis, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("redis", func(stdcontext.Context) error { return cache.Close() }); err != nil {
		return nil, errors.Join(err, cache.Close())
	}

	registry, err := etcdresource.Open(ctx, *cfg.Etcd, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("etcd", func(stdcontext.Context) error { return registry.Close() }); err != nil {
		return nil, errors.Join(err, registry.Close())
	}

	if err := addReadinessChecks(runtime.Health, cfg, db, cache, registry); err != nil {
		return nil, err
	}

	users, err := identityrepository.NewUserRepository(db)
	if err != nil {
		return nil, err
	}
	hasher, err := security.NewBcryptHasher(cfg.Bcrypt.Cost)
	if err != nil {
		return nil, err
	}
	register, err := identitylogic.NewRegisterLogic(users, hasher)
	if err != nil {
		return nil, err
	}
	handler, err := identityrpc.NewHandler(register, runtime.Health, runtime.Logger)
	if err != nil {
		return nil, err
	}

	httpTLS, err := cfg.HTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load identity admin TLS configuration: %w", err)
	}
	admin, err := adminhttp.NewServer(
		ctx,
		*cfg.HTTP,
		httpTLS,
		runtime.Health,
		runtime.Metrics,
		runtime.Trace,
		runtime.Logger,
	)
	if err != nil {
		return nil, err
	}
	// Register the admin component first so reverse-order shutdown keeps its
	// readiness endpoint available while Kitex deregisters and drains.
	if err := runtime.AddComponent(admin); err != nil {
		return nil, err
	}

	rpcTLS, err := cfg.RPC.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load identity RPC TLS configuration: %w", err)
	}
	rpcServer, err := identityrpc.NewRPCServer(
		ctx,
		*cfg.RPC,
		rpcTLS,
		handler,
		registry.Registry,
		runtime.Trace,
		runtime.Logger,
		map[string]string{
			"environment": cfg.App.Environment,
			"version":     cfg.App.Version,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(rpcServer); err != nil {
		return nil, errors.Join(err, rpcServer.Shutdown(ctx))
	}

	runtime.Logger.InfoContext(ctx, "identity dependencies initialized",
		slog.String("component", "identity.context"),
		slog.String("event", "dependencies_ready"),
	)
	return &ServiceContext{
		Config:     cfg,
		Database:   db,
		Redis:      cache,
		Etcd:       registry,
		Register:   register,
		RPCHandler: handler,
		RPCServer:  rpcServer,
		Admin:      admin,
	}, nil
}

func addReadinessChecks(
	registry *health.Registry,
	cfg config.Config,
	db *gorm.DB,
	cache *redisresource.Resource,
	etcd *etcdresource.Resources,
) error {
	return errors.Join(
		registry.AddReadiness("postgres", withTimeout(cfg.PostgreSQL.ConnectTimeout, func(ctx stdcontext.Context) error {
			return postgres.Ping(ctx, db)
		})),
		registry.AddReadiness("redis", withTimeout(cfg.Redis.ReadTimeout, cache.Ping)),
		registry.AddReadiness("etcd", etcd.Ping),
	)
}

func withTimeout(timeout time.Duration, check health.Check) health.Check {
	return func(ctx stdcontext.Context) error {
		checkCtx, cancel := stdcontext.WithTimeout(ctx, timeout)
		defer cancel()
		return check(checkCtx)
	}
}

package context

import (
	stdcontext "context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/postgres"
	hertztransport "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/hertz"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/migration"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/service"
	platformrpc "github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/transport/rpc"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/worker"
	"gorm.io/gorm"
)

type ServiceContext struct {
	Config   config.Config
	Database *gorm.DB
	NATS     *natsresource.DurableBroker
	Store    *repository.Store
	Service  *service.Service
	Worker   *worker.Worker
	RPC      *platformrpc.RPCServer
	Admin    *hertztransport.AdminServer
}

func NewServiceContext(ctx stdcontext.Context, cfg config.Config, runtime *coreapp.Runtime) (*ServiceContext, error) {
	if ctx == nil || runtime == nil || runtime.Logger == nil || runtime.Health == nil || runtime.Metrics == nil || runtime.Trace == nil {
		return nil, errors.New("create platform service context: context and initialized runtime are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("create platform service context: %w", err)
	}
	db, err := postgres.Open(ctx, *cfg.PostgreSQL, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("postgres", func(stdcontext.Context) error { return postgres.Close(db) }); err != nil {
		return nil, errors.Join(err, postgres.Close(db))
	}
	if err := migration.Apply(ctx, db); err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get platform postgres pool: %w", err)
	}
	if err := runtime.Metrics.RegisterDBStats("platform", sqlDB); err != nil {
		return nil, err
	}
	events, err := natsresource.OpenDurable(ctx, *cfg.NATS, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("nats", func(stdcontext.Context) error { return events.Close() }); err != nil {
		return nil, errors.Join(err, events.Close())
	}
	if err := events.EnsureStream(ctx, natsresource.StreamConfig{Name: cfg.Sync.Stream, Subjects: []string{cfg.Sync.Subject}, MaxAge: 7 * 24 * time.Hour, MaxBytes: 64 << 20, DuplicateWindow: 2 * time.Minute}); err != nil {
		return nil, err
	}
	if err := addReadiness(runtime.Health, cfg, db, events); err != nil {
		return nil, err
	}
	store, err := repository.New(db, cfg.App.Environment, cfg.Sync.Subject, cfg.Encryption.KeyID, cfg.Encryption.KEK)
	if err != nil {
		return nil, err
	}
	svc, err := service.New(store)
	if err != nil {
		return nil, err
	}
	verifier, err := coreauth.NewVerifier(cfg.Auth.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("create platform access-token verifier: %w", err)
	}
	handler, err := platformrpc.NewHandler(svc, verifier, runtime.Health, runtime.Logger, cfg.Auth.InternalToken)
	if err != nil {
		return nil, err
	}
	adminTLS, err := cfg.AdminHTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load platform admin TLS configuration: %w", err)
	}
	admin, err := hertztransport.NewAdminServer(ctx, hertztransport.AdminServerConfig{ComponentName: "platform-admin-http", LogComponent: "platform.admin", Options: *cfg.AdminHTTP, TLSConfig: adminTLS}, runtime.Health, runtime.Metrics, runtime.Trace, runtime.Logger, runtime.Requests)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(admin); err != nil {
		return nil, errors.Join(err, admin.Shutdown(ctx))
	}
	outboxWorker, err := worker.New(ctx, store, events, cfg.Sync.PollInterval, cfg.Sync.Lease, cfg.Sync.MaxAttempts, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(outboxWorker); err != nil {
		return nil, errors.Join(err, outboxWorker.Shutdown(ctx))
	}
	rpcTLS, err := cfg.RPC.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load platform RPC TLS configuration: %w", err)
	}
	rpcServer, err := platformrpc.NewRPCServer(ctx, *cfg.RPC, rpcTLS, handler, runtime.Trace, runtime.Metrics, runtime.Logger, runtime.Requests)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(rpcServer); err != nil {
		return nil, errors.Join(err, rpcServer.Shutdown(ctx))
	}
	runtime.Logger.InfoContext(ctx, "platform dependencies initialized", slog.String("component", "platform.context"), slog.String("event", "dependencies_ready"))
	return &ServiceContext{Config: cfg, Database: db, NATS: events, Store: store, Service: svc, Worker: outboxWorker, RPC: rpcServer, Admin: admin}, nil
}

func addReadiness(registry *health.Registry, cfg config.Config, db *gorm.DB, events *natsresource.DurableBroker) error {
	return errors.Join(
		registry.AddReadiness("postgres", withTimeout(cfg.PostgreSQL.ConnectTimeout, func(ctx stdcontext.Context) error { return postgres.Ping(ctx, db) })),
		registry.AddReadiness("nats", withTimeout(cfg.NATS.RequestTimeout, events.Ping)),
	)
}

func withTimeout(timeout time.Duration, check health.Check) health.Check {
	return func(ctx stdcontext.Context) error {
		checkCtx, cancel := stdcontext.WithTimeout(ctx, timeout)
		defer cancel()
		return check(checkCtx)
	}
}

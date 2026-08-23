package context

import (
	stdcontext "context"
	"errors"
	"fmt"
	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/postgres"
	hertztransport "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/hertz"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/migration"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/scanner"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/service"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/storage"
	attachmentrpc "github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/transport/rpc"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/worker"
	"gorm.io/gorm"
	"log/slog"
	"time"
)

type ServiceContext struct {
	Config   config.Config
	Database *gorm.DB
	Objects  *storage.S3
	Scanner  *scanner.ClamAV
	Store    *repository.Store
	Service  *service.Service
	RPC      *attachmentrpc.RPCServer
	Admin    *hertztransport.AdminServer
	Worker   *worker.Worker
}

func NewServiceContext(ctx stdcontext.Context, cfg config.Config, runtime *coreapp.Runtime) (*ServiceContext, error) {
	if ctx == nil || runtime == nil || runtime.Logger == nil || runtime.Health == nil || runtime.Metrics == nil || runtime.Trace == nil {
		return nil, errors.New("attachment context requires initialized runtime")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := postgres.Open(ctx, *cfg.PostgreSQL, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("postgres", func(stdcontext.Context) error { return postgres.Close(db) }); err != nil {
		return nil, err
	}
	if err := migration.Apply(ctx, db); err != nil {
		return nil, err
	}
	objects, err := storage.Open(ctx, *cfg.ObjectStorage)
	if err != nil {
		return nil, err
	}
	scan := scanner.New(cfg.Scanner.Address, cfg.Scanner.DialTimeout, cfg.Scanner.ScanTimeout, cfg.Scanner.MaximumStream)
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	svc, err := service.New(store, objects, scan, cfg.ObjectStorage.UploadTTL)
	if err != nil {
		return nil, err
	}
	verifier, err := coreauth.NewVerifier(cfg.Auth.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("create attachment verifier: %w", err)
	}
	if err := runtime.Health.AddReadiness("postgres", func(c stdcontext.Context) error { return postgres.Ping(c, db) }); err != nil {
		return nil, err
	}
	if err := runtime.Health.AddReadiness("object-storage", objects.Ping); err != nil {
		return nil, err
	}
	if err := runtime.Health.AddReadiness("clamav", scan.Ping); err != nil {
		return nil, err
	}
	handler, err := attachmentrpc.NewHandler(svc, verifier, runtime.Health, runtime.Logger)
	if err != nil {
		return nil, err
	}
	rpcTLS, err := cfg.RPC.TLS.ServerTLSConfig()
	if err != nil {
		return nil, err
	}
	rpcServer, err := attachmentrpc.NewRPCServer(ctx, *cfg.RPC, rpcTLS, handler, runtime.Trace, runtime.Metrics, runtime.Logger, runtime.Requests)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(rpcServer); err != nil {
		return nil, err
	}
	adminTLS, err := cfg.AdminHTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, err
	}
	admin, err := hertztransport.NewAdminServer(ctx, hertztransport.AdminServerConfig{ComponentName: "attachment-admin-http", LogComponent: "attachment.admin", Options: *cfg.AdminHTTP, TLSConfig: adminTLS}, runtime.Health, runtime.Metrics, runtime.Trace, runtime.Logger, runtime.Requests)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(admin); err != nil {
		return nil, err
	}
	w, err := worker.New(svc, 30*time.Second, 20*time.Minute, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(w); err != nil {
		return nil, err
	}
	runtime.Logger.InfoContext(ctx, "attachment dependencies initialized", slog.String("component", "attachment.context"))
	runtime.Health.SetServing(true)
	return &ServiceContext{Config: cfg, Database: db, Objects: objects, Scanner: scan, Store: store, Service: svc, RPC: rpcServer, Admin: admin, Worker: w}, nil
}
func (s *ServiceContext) ApplyDynamicConfig(cfg config.Config) error {
	if s == nil {
		return errors.New("attachment context is nil")
	}
	return nil
}

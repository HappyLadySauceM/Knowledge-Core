// Package context assembles Identity's process-scoped dependency graph.
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
	"github.com/HappyLadySauce/Knowledge-Core/pkg/postgres"
	redisresource "github.com/HappyLadySauce/Knowledge-Core/pkg/redis"
	hertztransport "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/hertz"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/config"
	identityemail "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/email"
	identitylogic "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/logic"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/migration"
	identityrepository "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/rpc"
	"gorm.io/gorm"
)

type ServiceContext struct {
	Config       config.Config
	Database     *gorm.DB
	Redis        *redisresource.Resource
	Register     *identitylogic.RegisterLogic
	Authenticate *identitylogic.AuthenticateLogic
	Sessions     *identitylogic.SessionLogic
	Actions      *identitylogic.ActionLogic
	Hasher       *security.BcryptHasher
	Issuer       *coreauth.Issuer
	GetUser      *identitylogic.GetUserLogic
	RPCHandler   *identityrpc.Handler
	RPCServer    *identityrpc.RPCServer
	Admin        *hertztransport.AdminServer
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

	cache, err := redisresource.Open(ctx, *cfg.Redis, "cache", runtime.Metrics, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("redis", func(stdcontext.Context) error { return cache.Close() }); err != nil {
		return nil, errors.Join(err, cache.Close())
	}

	if err := addReadinessChecks(runtime.Health, cfg, db, cache); err != nil {
		return nil, err
	}

	users, err := identityrepository.NewUserRepository(db)
	if err != nil {
		return nil, err
	}
	legacyKey := identitylogic.SessionPepper(cfg.Auth.PrivateKey)
	refreshKey := cfg.Auth.RefreshTokenEncryptionKey
	if refreshKey == "" {
		refreshKey = legacyKey
	}
	refreshPepper := cfg.Auth.RefreshTokenPepper
	if refreshPepper == "" {
		refreshPepper = legacyKey
	}
	actionPepper := cfg.Auth.ActionTokenPepper
	if actionPepper == "" {
		actionPepper = legacyKey
	}
	emailKey := cfg.Auth.EmailEncryptionKey
	if emailKey == "" {
		emailKey = legacyKey
	}
	sessions, err := identityrepository.NewSessionRepository(db, refreshKey)
	if err != nil {
		return nil, err
	}
	actionsRepo, err := identityrepository.NewActionRepository(db, emailKey)
	if err != nil {
		return nil, err
	}
	outbox, err := identityrepository.NewEmailOutboxRepository(db, emailKey)
	if err != nil {
		return nil, err
	}
	hasher, err := security.NewBcryptHasher(cfg.Bcrypt.Cost)
	if err != nil {
		return nil, err
	}
	register, err := identitylogic.NewRegisterLogic(users, hasher, actionsRepo, actionPepper, cfg.Auth.ActionTokenTTL)
	if err != nil {
		return nil, err
	}
	issuer, err := coreauth.NewIssuer(cfg.Auth.PrivateKey, cfg.Auth.AccessTokenTTL)
	if err != nil {
		return nil, fmt.Errorf("create identity access-token issuer: %w", err)
	}
	verifier, err := coreauth.NewVerifier(cfg.Auth.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("create identity access-token verifier: %w", err)
	}
	sessionLogic, err := identitylogic.NewSessionLogic(users, sessions, issuer, refreshPepper, cfg.Auth.RefreshTokenTTL, cfg.Auth.SessionIdleTTL)
	if err != nil {
		return nil, err
	}
	actionLogic, err := identitylogic.NewActionLogic(users, actionsRepo, sessions, hasher, actionPepper, cfg.Auth.ActionTokenTTL, outbox.Enqueue)
	if err != nil {
		return nil, err
	}
	authenticate, err := identitylogic.NewAuthenticateLogicWithSessions(
		users, hasher, issuer, sessionLogic, cfg.Auth.FailureThreshold, cfg.Auth.LockDuration,
	)
	if err != nil {
		return nil, err
	}
	getUser, err := identitylogic.NewGetUserLogic(users)
	if err != nil {
		return nil, err
	}
	handler, err := identityrpc.NewHandler(register, authenticate, sessionLogic, actionLogic, getUser, verifier, runtime.Health, runtime.Logger)
	if err != nil {
		return nil, err
	}
	worker, err := identityemail.NewWorker(*cfg.SMTP, outbox, emailKey, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if worker != nil {
		if err := runtime.AddComponent(worker); err != nil {
			return nil, err
		}
	}

	httpTLS, err := cfg.HTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load identity admin TLS configuration: %w", err)
	}
	admin, err := hertztransport.NewAdminServer(
		ctx,
		hertztransport.AdminServerConfig{
			ComponentName: "identity-admin-http",
			LogComponent:  "identity.admin",
			Options:       *cfg.HTTP,
			TLSConfig:     httpTLS,
		},
		runtime.Health,
		runtime.Metrics,
		runtime.Trace,
		runtime.Logger,
		runtime.Requests,
	)
	if err != nil {
		return nil, err
	}
	// Register the admin component first so reverse-order shutdown keeps its
	// readiness endpoint available while Kitex drains in-flight RPC.
	// 先注册 admin，逆序关闭时仍可在 Kitex 排空在途 RPC 期间提供 readiness。
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
		runtime.Trace,
		runtime.Metrics,
		runtime.Logger,
		runtime.Requests,
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
		Config:       cfg,
		Database:     db,
		Redis:        cache,
		Register:     register,
		Authenticate: authenticate,
		Sessions:     sessionLogic,
		Actions:      actionLogic,
		Hasher:       hasher,
		Issuer:       issuer,
		GetUser:      getUser,
		RPCHandler:   handler,
		RPCServer:    rpcServer,
		Admin:        admin,
	}, nil
}

func (s *ServiceContext) ApplyDynamicConfig(cfg config.Config) error {
	if s == nil || s.Hasher == nil || s.Issuer == nil || s.Authenticate == nil {
		return errors.New("apply identity dynamic configuration: service context is required")
	}
	return errors.Join(s.Hasher.SetCost(cfg.Bcrypt.Cost), s.Issuer.SetTTL(cfg.Auth.AccessTokenTTL), s.Authenticate.SetPolicy(cfg.Auth.FailureThreshold, cfg.Auth.LockDuration))
}

func addReadinessChecks(
	registry *health.Registry,
	cfg config.Config,
	db *gorm.DB,
	cache *redisresource.Resource,
) error {
	return errors.Join(
		registry.AddReadiness("postgres", withTimeout(cfg.PostgreSQL.ConnectTimeout, func(ctx stdcontext.Context) error {
			return postgres.Ping(ctx, db)
		})),
		registry.AddReadiness("redis", withTimeout(cfg.Redis.ReadTimeout, cache.Ping)),
	)
}

func withTimeout(timeout time.Duration, check health.Check) health.Check {
	return func(ctx stdcontext.Context) error {
		checkCtx, cancel := stdcontext.WithTimeout(ctx, timeout)
		defer cancel()
		return check(checkCtx)
	}
}

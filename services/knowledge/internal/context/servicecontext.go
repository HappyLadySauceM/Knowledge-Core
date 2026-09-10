// Package context assembles Knowledge's process-scoped dependency graph.
package context

import (
	stdcontext "context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/postgres"
	hertztransport "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/hertz"
	knowledgeclient "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/client"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	knowledgelogic "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/logic"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/migration"
	knowledgerepository "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/transport/rpc"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/worker"
	"gorm.io/gorm"
)

type ServiceContext struct {
	Config           config.Config
	Database         *gorm.DB
	NATS             *natsresource.DurableBroker
	Identity         identityservice.Client
	Directory        *knowledgeclient.Directory
	Collaboration    *knowledgeclient.Collaboration
	Attachment       *knowledgeclient.Attachment
	Store            *knowledgerepository.Store
	Documents        *knowledgelogic.DocumentLogic
	Members          *knowledgelogic.MemberLogic
	CollaborationAPI *knowledgelogic.CollaborationLogic
	RPCHandler       *knowledgerpc.Handler
	RPCServer        *knowledgerpc.RPCServer
	Workers          *worker.Worker
	Admin            *hertztransport.AdminServer
}

func NewServiceContext(ctx stdcontext.Context, cfg config.Config, runtime *coreapp.Runtime) (*ServiceContext, error) {
	if ctx == nil || runtime == nil || runtime.Logger == nil || runtime.Health == nil || runtime.Metrics == nil || runtime.Trace == nil {
		return nil, errors.New("create knowledge service context: context and initialized runtime are required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("create knowledge service context: %w", err)
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
		return nil, fmt.Errorf("get knowledge postgres pool for metrics: %w", err)
	}
	if err := runtime.Metrics.RegisterDBStats("knowledge", sqlDB); err != nil {
		return nil, err
	}

	events, err := natsresource.OpenDurable(ctx, *cfg.NATS, runtime.Logger)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddCleanup("nats", func(stdcontext.Context) error { return events.Close() }); err != nil {
		return nil, errors.Join(err, events.Close())
	}

	identity, err := knowledgeclient.NewIdentity(*cfg.IdentityRPC, runtime.Trace, runtime.Metrics)
	if err != nil {
		return nil, err
	}
	directory, err := knowledgeclient.NewDirectory(identity)
	if err != nil {
		return nil, err
	}
	collaboration, err := knowledgeclient.NewCollaboration(
		*cfg.CollaborationRPC, runtime.Trace, runtime.Metrics,
	)
	if err != nil {
		return nil, err
	}
	attachment, err := knowledgeclient.NewAttachment(
		*cfg.AttachmentRPC, runtime.Trace, runtime.Metrics,
	)
	if err != nil {
		return nil, err
	}
	verifier, err := coreauth.NewVerifier(cfg.Auth.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("create knowledge access-token verifier: %w", err)
	}
	store, err := knowledgerepository.NewStore(db)
	if err != nil {
		return nil, err
	}
	documents, err := knowledgelogic.NewDocumentLogic(store, directory)
	if err != nil {
		return nil, err
	}
	members, err := knowledgelogic.NewMemberLogic(store, directory)
	if err != nil {
		return nil, err
	}
	collaborationLogic, err := knowledgelogic.NewCollaborationLogic(store, directory)
	if err != nil {
		return nil, err
	}

	if err := addReadinessChecks(runtime.Health, cfg, db, events); err != nil {
		return nil, err
	}
	rpcHandler, err := knowledgerpc.NewHandler(
		documents, members, collaborationLogic, verifier, runtime.Health, runtime.Logger,
	)
	if err != nil {
		return nil, err
	}

	adminTLS, err := cfg.AdminHTTP.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load knowledge admin TLS configuration: %w", err)
	}
	admin, err := hertztransport.NewAdminServer(
		ctx,
		hertztransport.AdminServerConfig{
			ComponentName: "knowledge-admin-http", LogComponent: "knowledge.admin",
			Options: *cfg.AdminHTTP, TLSConfig: adminTLS,
		},
		runtime.Health, runtime.Metrics, runtime.Trace, runtime.Logger, runtime.Requests,
	)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(admin); err != nil {
		return nil, errors.Join(err, admin.Shutdown(ctx))
	}

	workers, err := worker.New(
		ctx, *cfg.Workers, sqlDB, store, attachment, events, collaboration, runtime.Logger,
	)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(workers); err != nil {
		return nil, errors.Join(err, workers.Shutdown(ctx))
	}

	rpcTLS, err := cfg.RPC.TLS.ServerTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("load knowledge RPC TLS configuration: %w", err)
	}
	rpcServer, err := knowledgerpc.NewRPCServer(
		ctx, *cfg.RPC, rpcTLS, rpcHandler, runtime.Trace, runtime.Metrics, runtime.Logger, runtime.Requests,
		map[string]string{"environment": cfg.App.Environment, "version": cfg.App.Version},
	)
	if err != nil {
		return nil, err
	}
	if err := runtime.AddComponent(rpcServer); err != nil {
		return nil, errors.Join(err, rpcServer.Shutdown(ctx))
	}

	runtime.Logger.InfoContext(ctx, "knowledge dependencies initialized",
		slog.String("component", "knowledge.context"), slog.String("event", "dependencies_ready"))
	return &ServiceContext{
		Config: cfg, Database: db, NATS: events,
		Identity: identity, Directory: directory, Collaboration: collaboration, Attachment: attachment,
		Store: store, Documents: documents, Members: members,
		CollaborationAPI: collaborationLogic, RPCHandler: rpcHandler,
		RPCServer: rpcServer,
		Workers:   workers, Admin: admin,
	}, nil
}

func (s *ServiceContext) ApplyDynamicConfig(cfg config.Config) error {
	if s == nil || s.Workers == nil {
		return errors.New("apply knowledge dynamic configuration: service context is required")
	}
	s.Workers.SetOptions(*cfg.Workers)
	return nil
}

func addReadinessChecks(
	registry *health.Registry,
	cfg config.Config,
	db *gorm.DB,
	events *natsresource.DurableBroker,
) error {
	return errors.Join(
		registry.AddReadiness("postgres", withTimeout(cfg.PostgreSQL.ConnectTimeout, func(ctx stdcontext.Context) error {
			return postgres.Ping(ctx, db)
		})),
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

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	internalbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/bootstrap"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/internal/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/internal/lifecycle"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	knowledgeapp "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/app"
	migrationpostgres "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/migration/postgres"
	knowledgepostgres "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository/postgres"
	knowledgekitex "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
)

const serviceName = "knowledge-core.knowledge"

func Run(ctx context.Context) (runErr error) {
	needs := internalbootstrap.Needs{Database: true, Cache: true, DurableMessaging: true, ConfigSource: true, Registry: true}
	cfg, err := internalbootstrap.LoadConfig("knowledge", "KC_KNOWLEDGE_RPC_ADDR", ":8882", needs)
	if err != nil {
		return err
	}
	cfg.Observability.Output = os.Stderr
	telemetry, err := observability.New(ctx, cfg.Observability)
	if err != nil {
		return err
	}
	telemetry.InstallCloudWeGo()
	logger := telemetry.Logger().With("component", "bootstrap")
	cleanup := func(closeCtx context.Context) error { return telemetry.Shutdown(closeCtx) }
	defer func() {
		if cleanup == nil {
			return
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		runErr = errors.Join(runErr, cleanup(closeCtx))
	}()
	accessTokenVerifier, err := auth.NewVerifier(os.Getenv("KC_KNOWLEDGE_JWT_PUBLIC_KEY"))
	if err != nil {
		return fmt.Errorf("configure knowledge access token verifier: %w", err)
	}
	if err := migrationpostgres.Up(ctx, cfg.Database.DSN); err != nil {
		return fmt.Errorf("migrate knowledge database: %w", err)
	}
	logger.InfoContext(ctx, "knowledge database migrations completed")
	resources, err := internalbootstrap.Open(ctx, cfg, needs)
	if err != nil {
		return err
	}
	cleanup = func(closeCtx context.Context) error {
		return errors.Join(resources.Close(closeCtx), telemetry.Shutdown(closeCtx))
	}
	documents := knowledgepostgres.NewDocumentRepository()
	application, err := knowledgeapp.NewService(resources.Database, documents, jsoncodec.New())
	if err != nil {
		return err
	}

	address, err := net.ResolveTCPAddr("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("resolve knowledge RPC address: %w", err)
	}
	exitSignal := make(chan error, 1)
	rpcServer := knowledgeservice.NewServer(
		knowledgekitex.NewHandler(application, accessTokenVerifier),
		server.WithServiceAddr(address),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: serviceName}),
		server.WithRegistry(resources.Registry),
		server.WithExitWaitTime(cfg.ShutdownTimeout),
		server.WithExitSignal(func() <-chan error { return exitSignal }),
		server.WithMiddleware(observability.KitexServerMiddleware(telemetry)),
	)
	managedCleanup := cleanup
	cleanup = nil
	runErr = lifecycle.Run(ctx, cfg.ShutdownTimeout, lifecycle.Process{
		SetServing: func(serving bool) {
			resources.SetServing(serving)
			phase := "draining"
			if serving {
				phase = "ready"
			}
			logger.InfoContext(ctx, "knowledge lifecycle changed", "event", "application", "phase", phase, "address", cfg.ListenAddress)
		},
		Serve: func() error {
			if serveErr := rpcServer.Run(); serveErr != nil {
				return fmt.Errorf("run knowledge RPC server: %w", serveErr)
			}
			return nil
		},
		Shutdown: func(context.Context) error {
			select {
			case exitSignal <- nil:
			default:
			}
			return nil
		},
		Close: managedCleanup,
	})
	logger.InfoContext(context.Background(), "knowledge RPC server stopped", "event", "application", "phase", "stopped")
	return runErr
}

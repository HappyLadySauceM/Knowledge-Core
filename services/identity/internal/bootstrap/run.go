package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	internalbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/bootstrap"
	"github.com/HappyLadySauce/Knowledge-Core/internal/lifecycle"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	identityapp "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/app"
	migrationpostgres "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/migration/postgres"
	identitypostgres "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository/postgres"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	identitykitex "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
)

const serviceName = "knowledge-core.identity"

func Run(ctx context.Context) (runErr error) {
	needs := internalbootstrap.Needs{Database: true, Cache: true, DurableMessaging: true, ConfigSource: true, Registry: true}
	cfg, err := internalbootstrap.LoadConfig("identity", "KC_IDENTITY_RPC_ADDR", ":8881", needs)
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
	accessTokens, err := security.NewAccessTokenIssuer(os.Getenv("KC_IDENTITY_JWT_PRIVATE_KEY"))
	if err != nil {
		return fmt.Errorf("configure identity access tokens: %w", err)
	}
	if err := migrationpostgres.Up(ctx, cfg.Database.DSN); err != nil {
		return fmt.Errorf("migrate identity database: %w", err)
	}
	logger.InfoContext(ctx, "identity database migrations completed")
	resources, err := internalbootstrap.Open(ctx, cfg, needs)
	if err != nil {
		return err
	}
	cleanup = func(closeCtx context.Context) error {
		return errors.Join(resources.Close(closeCtx), telemetry.Shutdown(closeCtx))
	}
	users, err := identitypostgres.NewUserRepository(resources.Database)
	if err != nil {
		return err
	}
	passwords, err := security.NewBcryptHasher(security.DefaultBcryptCost)
	if err != nil {
		return err
	}
	application, err := identityapp.NewService(users, passwords, accessTokens)
	if err != nil {
		return err
	}

	address, err := net.ResolveTCPAddr("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("resolve identity RPC address: %w", err)
	}
	exitSignal := make(chan error, 1)
	rpcServer := identityservice.NewServer(
		identitykitex.NewHandler(application),
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
			logger.InfoContext(ctx, "identity lifecycle changed", "event", "application", "phase", phase, "address", cfg.ListenAddress)
		},
		Serve: func() error {
			if serveErr := rpcServer.Run(); serveErr != nil {
				return fmt.Errorf("run identity RPC server: %w", serveErr)
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
	logger.InfoContext(context.Background(), "identity RPC server stopped", "event", "application", "phase", "stopped")
	return runErr
}

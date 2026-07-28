package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"

	foundationbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/bootstrap"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/lifecycle"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform/platformservice"
	platformkitex "github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	"github.com/cloudwego/kitex/server"
)

const serviceName = "knowledge-core.platform"

func Run(ctx context.Context) (runErr error) {
	needs := foundationbootstrap.Needs{Database: true, Cache: true, DurableMessaging: true, ConfigSource: true, Registry: true}
	cfg, err := foundationbootstrap.LoadConfig("platform", "KC_PLATFORM_RPC_ADDR", ":8883", needs)
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
	resources, err := foundationbootstrap.Open(ctx, cfg, needs)
	if err != nil {
		return err
	}
	cleanup = func(closeCtx context.Context) error {
		return errors.Join(resources.Close(closeCtx), telemetry.Shutdown(closeCtx))
	}

	address, err := net.ResolveTCPAddr("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("resolve platform RPC address: %w", err)
	}
	exitSignal := make(chan error, 1)
	rpcServer := platformservice.NewServer(
		platformkitex.NewHandler(),
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
			logger.InfoContext(ctx, "platform lifecycle changed", "event", "application", "phase", phase, "address", cfg.ListenAddress)
		},
		Serve: func() error {
			if serveErr := rpcServer.Run(); serveErr != nil {
				return fmt.Errorf("run platform RPC server: %w", serveErr)
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
	logger.InfoContext(context.Background(), "platform RPC server stopped", "event", "application", "phase", "stopped")
	return runErr
}

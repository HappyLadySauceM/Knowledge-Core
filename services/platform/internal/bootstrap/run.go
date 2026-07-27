package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"

	foundationbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/bootstrap"
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
	logger := observability.NewJSONLogger(os.Stderr, cfg.LogLevel, cfg.Service)
	observability.InstallCloudWeGoLoggers(os.Stderr, cfg.LogLevel, cfg.Service)
	resources, err := foundationbootstrap.Open(ctx, cfg, needs)
	if err != nil {
		return err
	}
	defer func() {
		resources.SetServing(false)
		closeCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		runErr = errors.Join(runErr, resources.Close(closeCtx))
	}()

	address, err := net.ResolveTCPAddr("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("resolve platform RPC address: %w", err)
	}
	rpcServer := platformservice.NewServer(
		platformkitex.NewHandler(),
		server.WithServiceAddr(address),
		server.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{ServiceName: serviceName}),
		server.WithRegistry(resources.Registry),
		server.WithExitWaitTime(cfg.ShutdownTimeout),
	)
	resources.SetServing(true)
	logger.LogAttrs(ctx, slog.LevelInfo, "platform RPC server starting", slog.String("address", cfg.ListenAddress))
	if err := rpcServer.Run(); err != nil {
		return fmt.Errorf("run platform RPC server: %w", err)
	}
	logger.InfoContext(ctx, "platform RPC server stopped")
	return nil
}

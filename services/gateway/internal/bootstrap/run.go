package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"os"

	foundationbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/bootstrap"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/router"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app/server"
)

func Run(ctx context.Context) (runErr error) {
	needs := foundationbootstrap.Needs{
		Cache:             true,
		DurableMessaging:  true,
		RealtimeMessaging: true,
		ConfigSource:      true,
		Resolver:          true,
	}
	cfg, err := foundationbootstrap.LoadConfig("gateway", "KC_GATEWAY_HTTP_ADDR", ":8080", needs)
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

	hertz := server.Default(
		server.WithHostPorts(cfg.ListenAddress),
		server.WithExitWaitTime(cfg.ShutdownTimeout),
		server.WithMaxRequestBodySize(4<<20),
	)
	hertz.Use(middleware.RequestID(), middleware.Dependencies(resources.Health))
	router.GeneratedRegister(hertz)

	resources.SetServing(true)
	logger.LogAttrs(ctx, slog.LevelInfo, "gateway starting", slog.String("address", cfg.ListenAddress))
	hertz.Spin()
	logger.InfoContext(ctx, "gateway stopped")
	return nil
}

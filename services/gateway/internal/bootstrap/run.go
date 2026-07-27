package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	foundationauth "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/auth"
	foundationbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/bootstrap"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/router"
	gatewayclient "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/client"
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
	accessTokenVerifier, err := foundationauth.NewVerifier(os.Getenv("KC_GATEWAY_JWT_PUBLIC_KEY"))
	if err != nil {
		return fmt.Errorf("configure gateway access tokens: %w", err)
	}
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
	identityClient, err := gatewayclient.NewIdentity(resources.Resolver)
	if err != nil {
		return fmt.Errorf("create gateway Identity client: %w", err)
	}
	if err := resources.Health.Add("identity-rpc", func(checkCtx context.Context) error {
		response, pingErr := identityClient.Ping(checkCtx, &common.PingRequest{})
		if pingErr != nil {
			return pingErr
		}
		if response == nil || response.Status != "ok" {
			return errors.New("identity RPC health check returned an invalid response")
		}
		return nil
	}); err != nil {
		return err
	}

	hertz := server.Default(
		server.WithHostPorts(cfg.ListenAddress),
		server.WithExitWaitTime(cfg.ShutdownTimeout),
		server.WithMaxRequestBodySize(4<<20),
	)
	hertz.Use(
		middleware.RequestID(),
		middleware.Authentication(accessTokenVerifier),
		middleware.Dependencies(middleware.RuntimeDependencies{Health: resources.Health, Identity: identityClient}),
	)
	router.GeneratedRegister(hertz)

	resources.SetServing(true)
	logger.LogAttrs(ctx, slog.LevelInfo, "gateway starting", slog.String("address", cfg.ListenAddress))
	hertz.Spin()
	logger.InfoContext(ctx, "gateway stopped")
	return nil
}

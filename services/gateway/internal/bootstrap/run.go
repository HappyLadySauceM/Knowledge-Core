package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	internalbootstrap "github.com/HappyLadySauce/Knowledge-Core/internal/bootstrap"
	"github.com/HappyLadySauce/Knowledge-Core/internal/lifecycle"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/router"
	gatewayclient "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/client"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

func Run(ctx context.Context) (runErr error) {
	needs := internalbootstrap.Needs{
		Cache:             true,
		DurableMessaging:  true,
		RealtimeMessaging: true,
		ConfigSource:      true,
		Resolver:          true,
	}
	cfg, err := internalbootstrap.LoadConfig("gateway", "KC_GATEWAY_HTTP_ADDR", ":8080", needs)
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
	accessTokenVerifier, err := auth.NewVerifier(os.Getenv("KC_GATEWAY_JWT_PUBLIC_KEY"))
	if err != nil {
		return fmt.Errorf("configure gateway access tokens: %w", err)
	}
	resources, err := internalbootstrap.Open(ctx, cfg, needs)
	if err != nil {
		return err
	}
	cleanup = func(closeCtx context.Context) error {
		return errors.Join(resources.Close(closeCtx), telemetry.Shutdown(closeCtx))
	}
	identityClient, err := gatewayclient.NewIdentity(resources.Resolver, telemetry)
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
	knowledgeClient, err := gatewayclient.NewKnowledge(resources.Resolver, telemetry)
	if err != nil {
		return fmt.Errorf("create gateway Knowledge client: %w", err)
	}
	if err := resources.Health.Add("knowledge-rpc", func(checkCtx context.Context) error {
		response, pingErr := knowledgeClient.Ping(checkCtx, &common.PingRequest{})
		if pingErr != nil {
			return pingErr
		}
		if response == nil || response.Status != "ok" {
			return errors.New("knowledge RPC health check returned an invalid response")
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
	if err := router.Register(hertz, router.Config{
		Logger: telemetry.Logger(),
		Tracing: observability.HertzServerMiddleware(telemetry, func(_ context.Context, request *app.RequestContext) bool {
			return string(request.Path()) == "/health/live" || string(request.Path()) == "/health/ready"
		}),
		Verifier: accessTokenVerifier,
		Dependencies: middleware.RuntimeDependencies{
			Health: resources.Health, Identity: identityClient, Knowledge: knowledgeClient,
		},
	}); err != nil {
		return err
	}

	managedCleanup := cleanup
	cleanup = nil
	runErr = lifecycle.Run(ctx, cfg.ShutdownTimeout, lifecycle.Process{
		SetServing: func(serving bool) {
			resources.SetServing(serving)
			phase := "draining"
			if serving {
				phase = "ready"
			}
			logger.InfoContext(ctx, "gateway lifecycle changed", "event", "application", "phase", phase, "address", cfg.ListenAddress)
		},
		Serve:    hertz.Run,
		Shutdown: hertz.Shutdown,
		Close:    managedCleanup,
	})
	logger.InfoContext(context.Background(), "gateway stopped", "event", "application", "phase", "stopped")
	return runErr
}

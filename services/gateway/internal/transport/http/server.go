package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/router"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network/standard"
)

const componentName = "gateway-public-http"

type Server struct {
	server   *server.Hertz
	listener net.Listener
}

func NewServer(
	ctx context.Context,
	options option.HertzServerOptions,
	tlsConfig *tls.Config,
	dependencies *gatewaymiddleware.Dependencies,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
) (*Server, error) {
	if ctx == nil || dependencies == nil || telemetry == nil || metricsRegistry == nil {
		return nil, errors.New("create gateway public server: context, dependencies, tracing, and metrics are required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create gateway public server: invalid options: %w", err)
	}
	if options.TLS.Enabled != (tlsConfig != nil) {
		return nil, errors.New("create gateway public server: TLS configuration does not match enabled setting")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Address)
	if err != nil {
		return nil, fmt.Errorf("listen for gateway public HTTP: %w", err)
	}
	hertzOptions := []config.Option{
		server.WithListener(listener),
		server.WithTransport(standard.NewTransporter),
		server.WithReadTimeout(options.ReadTimeout),
		server.WithWriteTimeout(options.WriteTimeout),
		server.WithIdleTimeout(options.IdleTimeout),
		server.WithExitWaitTime(options.ShutdownTimeout),
		server.WithMaxRequestBodySize(options.MaxRequestBodySize),
		server.WithHandleMethodNotAllowed(true),
		server.WithDisablePrintRoute(true),
	}
	if tlsConfig != nil {
		hertzOptions = append(hertzOptions, server.WithTLS(tlsConfig))
	}

	h := server.New(hertzOptions...)
	h.Use(
		coretrace.HertzServerMiddleware(telemetry, nil),
		metrics.HertzServerMiddleware(metricsRegistry, nil),
		gatewaymiddleware.Inject(dependencies),
		gatewaymiddleware.Recovery(),
		gatewaymiddleware.AccessLog(),
		gatewaymiddleware.SecurityHeaders(),
		gatewaymiddleware.CORS(),
		gatewaymiddleware.GlobalRateLimit(),
		gatewaymiddleware.OptionalAuthentication(),
	)
	h.NoRoute(func(ctx context.Context, request *app.RequestContext) {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrRouteNotFound)
	})
	h.NoMethod(func(ctx context.Context, request *app.RequestContext) {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrMethodNotAllowed)
	})
	router.GeneratedRegister(h)
	return &Server{server: h, listener: listener}, nil
}

func (s *Server) Name() string { return componentName }

func (s *Server) Serve() error {
	if s == nil || s.server == nil {
		return errors.New("serve gateway public server: server is nil")
	}
	return s.server.Run()
}

func (s *Server) Ready(ctx context.Context) error {
	if s == nil || s.server == nil {
		return errors.New("wait for gateway public readiness: server is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if s.server.IsRunning() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for gateway public readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	if !s.server.IsRunning() {
		return closeListener(s.listener)
	}
	return errors.Join(s.server.Shutdown(ctx), closeListener(s.listener))
}

func closeListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

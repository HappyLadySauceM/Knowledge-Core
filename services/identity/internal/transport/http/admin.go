package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/adaptor"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const componentName = "identity-admin-http"

type Server struct {
	server   *server.Hertz
	listener net.Listener
}

func NewServer(
	ctx context.Context,
	options option.HertzServerOptions,
	tlsConfig *tls.Config,
	healthRegistry *health.Registry,
	metricsRegistry *metrics.Registry,
	telemetry *coretrace.Runtime,
	logger *slog.Logger,
) (*Server, error) {
	if ctx == nil || healthRegistry == nil || metricsRegistry == nil || telemetry == nil || logger == nil {
		return nil, errors.New("create identity admin server: context, health, metrics, tracing, and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create identity admin server: invalid options: %w", err)
	}
	if options.TLS.Enabled != (tlsConfig != nil) {
		return nil, errors.New("create identity admin server: TLS configuration does not match enabled setting")
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Address)
	if err != nil {
		return nil, fmt.Errorf("listen for identity admin HTTP: %w", err)
	}
	hertzOptions := []config.Option{
		server.WithListener(listener),
		server.WithTransport(standard.NewTransporter),
		server.WithReadTimeout(options.ReadTimeout),
		server.WithWriteTimeout(options.WriteTimeout),
		server.WithIdleTimeout(options.IdleTimeout),
		server.WithExitWaitTime(options.ShutdownTimeout),
		server.WithMaxRequestBodySize(options.MaxRequestBodySize),
		server.WithDisablePrintRoute(true),
	}
	if tlsConfig != nil {
		hertzOptions = append(hertzOptions, server.WithTLS(tlsConfig))
	}

	h := server.New(hertzOptions...)
	h.Use(
		coretrace.HertzServerMiddleware(telemetry, isMetricsRequest),
		metrics.HertzServerMiddleware(metricsRegistry, isMetricsRequest),
		accessLogMiddleware(logger, isMetricsRequest),
		recoveryMiddleware(logger),
	)
	h.GET("/livez", healthHandler(healthRegistry.Live))
	h.GET("/readyz", healthHandler(healthRegistry.Ready))
	h.GET("/metrics", adaptor.HertzHandler(metricsRegistry.Handler()))
	return &Server{server: h, listener: listener}, nil
}

func (s *Server) Name() string { return componentName }

func (s *Server) Serve() error {
	if s == nil || s.server == nil {
		return errors.New("serve identity admin server: server is nil")
	}
	return s.server.Run()
}

func (s *Server) Ready(ctx context.Context) error {
	if s == nil || s.server == nil {
		return errors.New("wait for identity admin readiness: server is nil")
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
			return fmt.Errorf("wait for identity admin readiness: %w", ctx.Err())
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

type statusResponse struct {
	Status    string `json:"status"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
}

func healthHandler(check health.Check) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		if err := check(ctx); err != nil {
			writeStatus(ctx, request, consts.StatusServiceUnavailable, "unavailable")
			return
		}
		writeStatus(ctx, request, consts.StatusOK, "ok")
	}
}

func writeStatus(ctx context.Context, request *app.RequestContext, code int, status string) {
	payload, err := jsoncodec.Marshal(statusResponse{
		Status:    status,
		RequestID: metadata.RequestID(ctx),
		TraceID:   coretrace.TraceID(ctx),
	})
	if err != nil {
		request.Data(consts.StatusInternalServerError, consts.MIMEApplicationJSONUTF8, []byte(`{"status":"error"}`))
		return
	}
	request.Data(code, consts.MIMEApplicationJSONUTF8, payload)
}

func accessLogMiddleware(logger *slog.Logger, ignore func(context.Context, *app.RequestContext) bool) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		started := time.Now()
		request.Next(ctx)
		if ignore != nil && ignore(ctx, request) {
			return
		}
		logger.InfoContext(ctx, "HTTP request completed",
			slog.String("component", "identity.admin"),
			slog.String("event", "request"),
			slog.String("http.method", string(request.Method())),
			slog.String("http.route", request.FullPath()),
			slog.Int("http.status_code", request.Response.StatusCode()),
			slog.Duration("duration", time.Since(started)),
		)
	}
}

func isMetricsRequest(_ context.Context, request *app.RequestContext) bool {
	return string(request.Request.URI().Path()) == "/metrics"
}

func recoveryMiddleware(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(ctx, "HTTP handler panic recovered",
					slog.String("component", "identity.admin"),
					slog.String("event", "panic"),
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
				)
				writeStatus(ctx, request, consts.StatusInternalServerError, "error")
			}
		}()
		request.Next(ctx)
	}
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

package internalhttp

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
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/config"
	"github.com/cloudwego/hertz/pkg/network/standard"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const componentName = "knowledge-internal-http"

type Server struct {
	server   *server.Hertz
	listener net.Listener
}

func NewServer(
	ctx context.Context,
	options option.HertzServerOptions,
	tlsConfig *tls.Config,
	handler *Handler,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
	logger *slog.Logger,
) (*Server, error) {
	if ctx == nil || handler == nil || telemetry == nil || metricsRegistry == nil || logger == nil {
		return nil, errors.New("create knowledge internal server: context, handler, tracing, metrics, and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create knowledge internal server: invalid options: %w", err)
	}
	if options.TLS.Enabled != (tlsConfig != nil) {
		return nil, errors.New("create knowledge internal server: TLS configuration does not match enabled setting")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Address)
	if err != nil {
		return nil, fmt.Errorf("listen for knowledge internal HTTP: %w", err)
	}
	hertzOptions := []config.Option{
		server.WithListener(listener), server.WithTransport(standard.NewTransporter),
		server.WithReadTimeout(options.ReadTimeout), server.WithWriteTimeout(options.WriteTimeout),
		server.WithIdleTimeout(options.IdleTimeout), server.WithExitWaitTime(options.ShutdownTimeout),
		server.WithMaxRequestBodySize(options.MaxRequestBodySize), server.WithHandleMethodNotAllowed(true),
		server.WithDisablePrintRoute(true),
	}
	if tlsConfig != nil {
		hertzOptions = append(hertzOptions, server.WithTLS(tlsConfig))
	}
	h := server.New(hertzOptions...)
	h.Use(
		coretrace.HertzServerMiddleware(telemetry, nil),
		metrics.HertzServerMiddleware(metricsRegistry, nil),
		recovery(logger), accessLog(logger),
	)
	h.GET("/health/live", func(_ context.Context, request *app.RequestContext) {
		writeJSON(request, consts.StatusOK, map[string]string{"status": "ok", "service": "knowledge"})
	})
	h.POST("/internal/v1/documents/:document_id/authorization", handler.Authorize)
	h.PUT("/internal/v1/documents/:document_id/projection", handler.Project)
	h.NoRoute(func(ctx context.Context, request *app.RequestContext) {
		writeStatusProblem(ctx, request, 404, knowledgeerrors.NotFound.New())
	})
	h.NoMethod(func(ctx context.Context, request *app.RequestContext) {
		writeStatusProblem(ctx, request, 405, knowledgeerrors.InvalidInput.New())
	})
	return &Server{server: h, listener: listener}, nil
}

func (s *Server) Name() string { return componentName }

func (s *Server) Serve() error {
	if s == nil || s.server == nil {
		return errors.New("serve knowledge internal server: server is nil")
	}
	return s.server.Run()
}

func (s *Server) Ready(ctx context.Context) error {
	if s == nil || s.server == nil {
		return errors.New("wait for knowledge internal readiness: server is nil")
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
			return fmt.Errorf("wait for knowledge internal readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	if !s.server.IsRunning() {
		return closeHTTPListener(s.listener)
	}
	return errors.Join(s.server.Shutdown(ctx), closeHTTPListener(s.listener))
}

func recovery(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(ctx, "knowledge internal handler panic recovered",
					slog.String("component", "knowledge.internal_http"), slog.String("event", "panic"),
					slog.Any("panic", recovered), slog.String("stack", string(debug.Stack())))
				apperror.WriteHertzError(ctx, request, knowledgeerrors.Internal.New())
			}
		}()
		request.Next(ctx)
	}
}

func accessLog(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		started := time.Now()
		request.Next(ctx)
		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		logger.InfoContext(ctx, "knowledge internal HTTP request completed",
			slog.String("component", "knowledge.internal_http"), slog.String("event", "request"),
			slog.String("http.method", string(request.Method())), slog.String("http.route", route),
			slog.Int("http.status_code", request.Response.StatusCode()), slog.Duration("duration", time.Since(started)))
	}
}

func writeStatusProblem(ctx context.Context, request *app.RequestContext, status int, err error) {
	problem := apperror.ToHTTPProblem(ctx, status, err)
	payload, marshalErr := jsonMarshal(problem)
	if marshalErr != nil {
		apperror.WriteHertzError(ctx, request, knowledgeerrors.Internal.New())
		return
	}
	request.Data(status, apperror.ProblemContentType, payload)
}

func jsonMarshal(value any) ([]byte, error) {
	return jsoncodec.Marshal(value)
}

func closeHTTPListener(listener net.Listener) error {
	if listener == nil {
		return nil
	}
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

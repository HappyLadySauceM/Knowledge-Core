// Package hertz contains reusable Hertz transport components.
package hertz

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"strings"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
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

type AdminServerConfig struct {
	ComponentName string
	LogComponent  string
	Options       option.HertzServerOptions
	TLSConfig     *tls.Config
}

type AdminServer struct {
	name         string
	logComponent string
	server       *server.Hertz
	listener     net.Listener
}

func NewAdminServer(
	ctx context.Context,
	cfg AdminServerConfig,
	healthRegistry *health.Registry,
	metricsRegistry *metrics.Registry,
	telemetry *coretrace.Runtime,
	logger *slog.Logger,
	requestLogs *corelog.RequestControl,
) (*AdminServer, error) {
	cfg.ComponentName = strings.TrimSpace(cfg.ComponentName)
	cfg.LogComponent = strings.TrimSpace(cfg.LogComponent)
	if ctx == nil || cfg.ComponentName == "" || cfg.LogComponent == "" || healthRegistry == nil || metricsRegistry == nil || telemetry == nil || logger == nil {
		return nil, errors.New("create admin server: context, names, health, metrics, tracing, and logger are required")
	}
	if err := cfg.Options.Validate(); err != nil {
		return nil, fmt.Errorf("create admin server: invalid options: %w", err)
	}
	if cfg.Options.TLS.Enabled != (cfg.TLSConfig != nil) {
		return nil, errors.New("create admin server: TLS configuration does not match enabled setting")
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Options.Address)
	if err != nil {
		return nil, fmt.Errorf("listen for admin HTTP: %w", err)
	}
	hertzOptions := []config.Option{
		server.WithListener(listener),
		server.WithTransport(standard.NewTransporter),
		server.WithReadTimeout(cfg.Options.ReadTimeout),
		server.WithWriteTimeout(cfg.Options.WriteTimeout),
		server.WithIdleTimeout(cfg.Options.IdleTimeout),
		server.WithExitWaitTime(cfg.Options.ShutdownTimeout),
		server.WithMaxRequestBodySize(cfg.Options.MaxRequestBodySize),
		server.WithDisablePrintRoute(true),
	}
	if cfg.TLSConfig != nil {
		hertzOptions = append(hertzOptions, server.WithTLS(cfg.TLSConfig))
	}

	h := server.New(hertzOptions...)
	result := &AdminServer{
		name: cfg.ComponentName, logComponent: cfg.LogComponent,
		server: h, listener: listener,
	}
	h.Use(
		coretrace.HertzServerMiddleware(telemetry, isMetricsRequest),
		metrics.HertzServerMiddleware(metricsRegistry, isMetricsRequest),
		result.accessLogMiddleware(logger, requestLogs),
		result.recoveryMiddleware(logger),
	)
	h.GET("/livez", healthHandler(healthRegistry.Live))
	h.GET("/readyz", healthHandler(healthRegistry.Ready))
	h.GET("/metrics", adaptor.HertzHandler(metricsRegistry.Handler()))
	return result, nil
}

func (s *AdminServer) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

func (s *AdminServer) Serve() error {
	if s == nil || s.server == nil {
		return errors.New("serve admin server: server is nil")
	}
	return s.server.Run()
}

func (s *AdminServer) Ready(ctx context.Context) error {
	if s == nil || s.server == nil {
		return errors.New("wait for admin readiness: server is nil")
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
			return fmt.Errorf("wait for admin readiness: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (s *AdminServer) Shutdown(ctx context.Context) error {
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
		Status: status, RequestID: metadata.RequestID(ctx), TraceID: coretrace.TraceID(ctx),
	})
	if err != nil {
		request.Data(consts.StatusInternalServerError, consts.MIMEApplicationJSONUTF8, []byte(`{"status":"error"}`))
		return
	}
	request.Data(code, consts.MIMEApplicationJSONUTF8, payload)
}

func (s *AdminServer) accessLogMiddleware(logger *slog.Logger, requestLogs *corelog.RequestControl) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		started := time.Now()
		request.Next(ctx)
		if isMetricsRequest(ctx, request) {
			return
		}
		status := request.Response.StatusCode()
		if status >= 200 && status < 300 && isHealthRequest(request) && !requestLogs.HealthCheckRequests() {
			return
		}
		logger.InfoContext(ctx, "HTTP request completed",
			slog.String("component", s.logComponent),
			slog.String("event", "request"),
			slog.String("http.method", string(request.Method())),
			slog.String("http.route", request.FullPath()),
			slog.Int("http.status_code", request.Response.StatusCode()),
			slog.Duration("duration", time.Since(started)),
		)
	}
}

func isHealthRequest(request *app.RequestContext) bool {
	path := string(request.Request.URI().Path())
	return path == "/livez" || path == "/readyz"
}

func (s *AdminServer) recoveryMiddleware(logger *slog.Logger) app.HandlerFunc {
	return func(ctx context.Context, request *app.RequestContext) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(ctx, "HTTP handler panic recovered",
					slog.String("component", s.logComponent),
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

func isMetricsRequest(_ context.Context, request *app.RequestContext) bool {
	return string(request.Request.URI().Path()) == "/metrics"
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

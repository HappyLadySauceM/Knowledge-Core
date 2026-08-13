package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/limit"
	"github.com/cloudwego/kitex/pkg/registry"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
	"github.com/cloudwego/kitex/pkg/rpcinfo"
	kitexserver "github.com/cloudwego/kitex/server"
)

const rpcComponentName = "identity-kitex-rpc"

type RPCServer struct {
	server       kitexserver.Server
	listener     net.Listener
	registration registrationWaiter
}

type registrationWaiter interface {
	WaitRegistered(context.Context) error
}

func NewRPCServer(
	ctx context.Context,
	options option.KitexServerOptions,
	tlsConfig *tls.Config,
	service identity.IdentityService,
	serviceRegistry registry.Registry,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
	logger *slog.Logger,
	requestLogs *corelog.RequestControl,
	tags map[string]string,
) (*RPCServer, error) {
	if ctx == nil || service == nil || serviceRegistry == nil || telemetry == nil || metricsRegistry == nil || logger == nil {
		return nil, errors.New("create identity RPC server: context, service, registry, tracing, metrics, and logger are required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create identity RPC server: invalid options: %w", err)
	}
	if options.TLS.Enabled != (tlsConfig != nil) {
		return nil, errors.New("create identity RPC server: TLS configuration does not match enabled setting")
	}
	registration, ok := serviceRegistry.(registrationWaiter)
	if !ok {
		return nil, errors.New("create identity RPC server: registry must expose a registration readiness handshake")
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Address)
	if err != nil {
		return nil, fmt.Errorf("listen for identity RPC: %w", err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}

	serverOptions := []kitexserver.Option{
		kitexserver.WithListener(listener),
		kitexserver.WithRegistry(serviceRegistry),
		kitexserver.WithServerBasicInfo(&rpcinfo.EndpointBasicInfo{
			ServiceName: options.ServiceName,
			Tags:        cloneTags(tags),
		}),
		kitexserver.WithReadWriteTimeout(options.ReadWriteTimeout),
		kitexserver.WithExitWaitTime(options.ExitWaitTimeout),
		kitexserver.WithCompatibleMiddlewareForUnary(),
	}
	serverOptions = append(serverOptions, coretrace.KitexServerOptions(telemetry)...)
	serverOptions = append(serverOptions, kitexserver.WithMiddleware(metrics.KitexServerMiddleware(metricsRegistry)))
	serverOptions = append(serverOptions, kitexserver.WithMiddleware(accessLogMiddleware(logger, requestLogs)))
	if tlsConfig != nil {
		// Kitex's Linux default netpoll transport cannot consume a tls.Listener.
		// The standard Go transport preserves Thrift/TTHeader while terminating
		// TLS at the pre-bound listener.
		serverOptions = append(serverOptions,
			kitexserver.WithTransServerFactory(gonet.NewTransServerFactory()),
			kitexserver.WithTransHandlerFactory(gonet.NewSvrTransHandlerFactory()),
		)
	}
	if options.MaxConnections > 0 {
		serverOptions = append(serverOptions, kitexserver.WithLimit(&limit.Option{MaxConnections: options.MaxConnections}))
	}

	kitex := kitexserver.NewServer(serverOptions...)
	if err := identityservice.RegisterService(kitex, service); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("register identity Kitex service: %w", err)
	}
	return &RPCServer{server: kitex, listener: listener, registration: registration}, nil
}

func (s *RPCServer) Name() string { return rpcComponentName }

func (s *RPCServer) Serve() error {
	if s == nil || s.server == nil {
		return errors.New("serve identity RPC server: server is nil")
	}
	return s.server.Run()
}

func (s *RPCServer) Ready(ctx context.Context) error {
	if s == nil || s.registration == nil {
		return errors.New("wait for identity RPC readiness: registration handshake is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.registration.WaitRegistered(ctx); err != nil {
		return fmt.Errorf("wait for identity RPC registration: %w", err)
	}
	return nil
}

func (s *RPCServer) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result := make(chan error, 1)
	go func() {
		result <- stopRPCServer(s.server, s.listener)
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = closeListener(s.listener)
		return ctx.Err()
	}
}

func stopRPCServer(server kitexserver.Server, listener net.Listener) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = errors.Join(err, fmt.Errorf("stop identity RPC server panic: %v\n%s", recovered, debug.Stack()))
		}
	}()
	return errors.Join(server.Stop(), closeListener(listener))
}

func accessLogMiddleware(logger *slog.Logger, requestLogs *corelog.RequestControl) endpoint.Middleware {
	return func(next endpoint.Endpoint) endpoint.Endpoint {
		return func(ctx context.Context, request, response any) error {
			started := time.Now()
			err := next(ctx, request, response)
			serviceName, methodName := rpcLogDetails(ctx)
			attributes := []any{
				slog.String("component", "identity.rpc"),
				slog.String("event", "request"),
				slog.String("rpc.service", serviceName),
				slog.String("rpc.method", methodName),
				slog.Duration("duration", time.Since(started)),
			}
			if err != nil {
				logger.ErrorContext(ctx, "RPC request failed", append(attributes, slog.Any("error", err))...)
			} else if businessError := rpcBusinessError(ctx); businessError != nil {
				logger.WarnContext(ctx, "RPC request completed with business error", append(attributes,
					slog.Int64("rpc.business_code", int64(businessError.BizStatusCode())),
					slog.String("rpc.business_message", businessError.BizMessage()),
				)...)
			} else if requestLogs.HealthCheckRequests() || (methodName != "Ping" && methodName != "Live") {
				logger.InfoContext(ctx, "RPC request completed", attributes...)
			}
			return err
		}
	}
}

func rpcBusinessError(ctx context.Context) interface {
	BizStatusCode() int32
	BizMessage() string
} {
	info := rpcinfo.GetRPCInfo(ctx)
	if info == nil || info.Invocation() == nil {
		return nil
	}
	return info.Invocation().BizStatusErr()
}

func rpcLogDetails(ctx context.Context) (string, string) {
	serviceName, methodName := "unknown", "unknown"
	info := rpcinfo.GetRPCInfo(ctx)
	if info == nil || info.Invocation() == nil {
		return serviceName, methodName
	}
	if value := info.Invocation().ServiceName(); value != "" {
		serviceName = value
	}
	if value := info.Invocation().MethodName(); value != "" {
		methodName = value
	}
	return serviceName, methodName
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

func cloneTags(tags map[string]string) map[string]string {
	clone := make(map[string]string, len(tags))
	for key, value := range tags {
		clone[key] = value
	}
	return clone
}

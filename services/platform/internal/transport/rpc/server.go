package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform/platformservice"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
	kitexserver "github.com/cloudwego/kitex/server"
)

type RPCServer struct {
	server   kitexserver.Server
	listener net.Listener
}

func NewRPCServer(ctx context.Context, options option.KitexServerOptions, tlsConfig *tls.Config, handler platformv1.PlatformService, trace *coretrace.Runtime, metricsRegistry *metrics.Registry, logger interface{}, logs *corelog.RequestControl) (*RPCServer, error) {
	if ctx == nil || handler == nil || trace == nil || metricsRegistry == nil {
		return nil, errors.New("platform RPC server dependencies are required")
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", options.Address)
	if err != nil {
		return nil, fmt.Errorf("listen platform RPC: %w", err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	serverOptions := []kitexserver.Option{kitexserver.WithListener(listener), kitexserver.WithReadWriteTimeout(options.ReadWriteTimeout), kitexserver.WithExitWaitTime(options.ExitWaitTimeout), kitexserver.WithCompatibleMiddlewareForUnary()}
	serverOptions = append(serverOptions, coretrace.KitexServerOptions(trace)...)
	if tlsConfig != nil {
		serverOptions = append(serverOptions, kitexserver.WithTransServerFactory(gonet.NewTransServerFactory()), kitexserver.WithTransHandlerFactory(gonet.NewSvrTransHandlerFactory()))
	}
	server := kitexserver.NewServer(serverOptions...)
	if err := platformservice.RegisterService(server, handler); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &RPCServer{server: server, listener: listener}, nil
}

func (s *RPCServer) Name() string { return "platform-rpc" }
func (s *RPCServer) Serve() error { return s.server.Run() }
func (s *RPCServer) Ready(context.Context) error {
	if s == nil || s.listener == nil {
		return errors.New("platform RPC is not listening")
	}
	return nil
}
func (s *RPCServer) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- s.server.Stop(); _ = s.listener.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = s.listener.Close()
		return ctx.Err()
	}
}

package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment/attachmentservice"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/cloudwego/kitex/pkg/remote/trans/gonet"
	kitexserver "github.com/cloudwego/kitex/server"
	"net"
)

type RPCServer struct {
	server   kitexserver.Server
	listener net.Listener
}

func NewRPCServer(ctx context.Context, opts option.KitexServerOptions, tlsConfig *tls.Config, handler attachment.AttachmentService, trace *coretrace.Runtime, metricsRegistry *metrics.Registry, logger interface{}, logs *corelog.RequestControl) (*RPCServer, error) {
	if ctx == nil || handler == nil || trace == nil || metricsRegistry == nil {
		return nil, errors.New("attachment RPC server dependencies are required")
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", opts.Address)
	if err != nil {
		return nil, fmt.Errorf("listen attachment RPC: %w", err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	options := []kitexserver.Option{kitexserver.WithListener(listener), kitexserver.WithReadWriteTimeout(opts.ReadWriteTimeout), kitexserver.WithExitWaitTime(opts.ExitWaitTimeout), kitexserver.WithCompatibleMiddlewareForUnary()}
	options = append(options, coretrace.KitexServerOptions(trace)...)
	if tlsConfig != nil {
		options = append(options, kitexserver.WithTransServerFactory(gonet.NewTransServerFactory()), kitexserver.WithTransHandlerFactory(gonet.NewSvrTransHandlerFactory()))
	}
	svr := kitexserver.NewServer(options...)
	if err := attachmentservice.RegisterService(svr, handler); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return &RPCServer{server: svr, listener: listener}, nil
}
func (s *RPCServer) Name() string { return "attachment-rpc" }
func (s *RPCServer) Serve() error { return s.server.Run() }
func (s *RPCServer) Ready(context.Context) error {
	if s == nil || s.listener == nil {
		return errors.New("attachment RPC is not listening")
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

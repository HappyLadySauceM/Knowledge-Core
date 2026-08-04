package client

import (
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration/collaborationservice"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	transportkitex "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/kitex"
	kitexclient "github.com/cloudwego/kitex/client"
	"github.com/cloudwego/kitex/pkg/discovery"
)

func NewCollaboration(
	options option.KitexClientOptions,
	resolver discovery.Resolver,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
) (collaborationservice.Client, error) {
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create Collaboration client: invalid options: %w", err)
	}
	if resolver == nil || telemetry == nil || metricsRegistry == nil {
		return nil, errors.New("create Collaboration client: resolver, tracing, and metrics are required")
	}
	tlsConfig, err := options.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create Collaboration client TLS: %w", err)
	}
	clientOptions := []kitexclient.Option{
		kitexclient.WithResolver(resolver),
		kitexclient.WithConnectTimeout(options.ConnectTimeout),
		kitexclient.WithRPCTimeout(options.RequestTimeout),
		kitexclient.WithMiddleware(metrics.KitexClientMiddleware(metricsRegistry)),
	}
	clientOptions = append(clientOptions, transportkitex.ClientOptions(telemetry, tlsConfig)...)
	result, err := collaborationservice.NewClient(options.ServiceName, clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Collaboration client: %w", err)
	}
	return result, nil
}

package client

import (
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity/identityservice"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	transportkitex "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/kitex"
	kitexclient "github.com/cloudwego/kitex/client"
)

func NewIdentity(
	opts option.KitexClientOptions,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
) (identityservice.Client, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("create Identity client: invalid options: %w", err)
	}
	if telemetry == nil || metricsRegistry == nil {
		return nil, errors.New("create Identity client: tracing and metrics are required")
	}
	tlsConfig, err := opts.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create Identity client TLS: %w", err)
	}
	clientOptions := []kitexclient.Option{
		kitexclient.WithHostPorts(opts.Address),
		kitexclient.WithConnectTimeout(opts.ConnectTimeout),
		kitexclient.WithRPCTimeout(opts.RequestTimeout),
		kitexclient.WithMiddleware(metrics.KitexClientMiddleware(metricsRegistry)),
	}
	clientOptions = append(clientOptions, transportkitex.ClientOptions(telemetry, tlsConfig)...)
	result, err := identityservice.NewClient(opts.ServiceName, clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Identity client: %w", err)
	}
	return result, nil
}

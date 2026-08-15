package client

import (
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge/knowledgeservice"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	transportkitex "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/kitex"
	kitexclient "github.com/cloudwego/kitex/client"
)

func NewKnowledge(
	opts option.KitexClientOptions,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
) (knowledgeservice.Client, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("create Knowledge client: invalid options: %w", err)
	}
	if telemetry == nil || metricsRegistry == nil {
		return nil, errors.New("create Knowledge client: tracing and metrics are required")
	}
	tlsConfig, err := opts.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create Knowledge client TLS: %w", err)
	}
	clientOptions := []kitexclient.Option{
		kitexclient.WithHostPorts(opts.Address),
		kitexclient.WithConnectTimeout(opts.ConnectTimeout),
		kitexclient.WithRPCTimeout(opts.RequestTimeout),
	}
	clientOptions = append(clientOptions, transportkitex.OutboundOptions(telemetry, tlsConfig, metricsRegistry, "knowledge")...)
	result, err := knowledgeservice.NewClient(opts.ServiceName, clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Knowledge client: %w", err)
	}
	return result, nil
}

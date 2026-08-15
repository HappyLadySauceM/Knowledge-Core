package client

import (
	"context"
	"errors"
	"fmt"

	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration/collaborationservice"
	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	transportkitex "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/kitex"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	kitexclient "github.com/cloudwego/kitex/client"
)

type Collaboration struct {
	client collaborationservice.Client
}

func NewCollaboration(
	options option.KitexClientOptions,
	telemetry *coretrace.Runtime,
	metricsRegistry *metrics.Registry,
) (*Collaboration, error) {
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create Collaboration client: invalid options: %w", err)
	}
	if telemetry == nil || metricsRegistry == nil {
		return nil, errors.New("create Collaboration client: tracing and metrics are required")
	}
	tlsConfig, err := options.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create Collaboration client TLS: %w", err)
	}
	clientOptions := []kitexclient.Option{
		kitexclient.WithHostPorts(options.Address),
		kitexclient.WithConnectTimeout(options.ConnectTimeout),
		kitexclient.WithRPCTimeout(options.RequestTimeout),
	}
	clientOptions = append(clientOptions, transportkitex.OutboundOptions(telemetry, tlsConfig, metricsRegistry, "collaboration")...)
	result, err := collaborationservice.NewClient(options.ServiceName, clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Collaboration client: %w", err)
	}
	return &Collaboration{client: result}, nil
}

func (c *Collaboration) Ping(ctx context.Context) error {
	if c == nil || c.client == nil {
		return errors.New("ping Collaboration: client is nil")
	}
	response, err := c.client.Ping(ctx, &commonv1.PingRequest{})
	if err != nil {
		return fmt.Errorf("ping Collaboration: %w", err)
	}
	if response == nil || response.Service != "collaboration" || response.Status != "ready" {
		return errors.New("ping Collaboration: service is not ready")
	}
	return nil
}

func (c *Collaboration) PurgeDocument(ctx context.Context, documentID string) error {
	if c == nil || c.client == nil {
		return errors.New("purge Collaboration document: client is nil")
	}
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return fmt.Errorf("purge Collaboration document: %w", err)
	}
	if err := c.client.PurgeDocument(ctx, &collaborationv1.PurgeDocumentRequest{DocumentId: documentID}); err != nil {
		return fmt.Errorf("purge Collaboration document: %w", err)
	}
	return nil
}

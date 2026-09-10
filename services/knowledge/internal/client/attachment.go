package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment/attachmentservice"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	transportkitex "github.com/HappyLadySauce/Knowledge-Core/pkg/transport/kitex"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	kitexclient "github.com/cloudwego/kitex/client"
)

type Attachment struct {
	client       attachmentservice.Client
	serviceToken string
}

func NewAttachment(options option.KitexClientOptions, serviceToken string, telemetry *coretrace.Runtime, metricsRegistry *metrics.Registry) (*Attachment, error) {
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create Attachment client: invalid options: %w", err)
	}
	if strings.TrimSpace(serviceToken) == "" || telemetry == nil || metricsRegistry == nil {
		return nil, errors.New("create Attachment client: service token, tracing, and metrics are required")
	}
	tlsConfig, err := options.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create Attachment client TLS: %w", err)
	}
	clientOptions := []kitexclient.Option{
		kitexclient.WithHostPorts(options.Address), kitexclient.WithConnectTimeout(options.ConnectTimeout),
		kitexclient.WithRPCTimeout(options.RequestTimeout),
	}
	clientOptions = append(clientOptions, transportkitex.OutboundOptions(telemetry, tlsConfig, metricsRegistry, "attachment")...)
	client, err := attachmentservice.NewClient(options.ServiceName, clientOptions...)
	if err != nil {
		return nil, fmt.Errorf("create Attachment client: %w", err)
	}
	return &Attachment{client: client, serviceToken: strings.TrimSpace(serviceToken)}, nil
}

func (c *Attachment) command(job domain.PublicationReferenceJob) *attachmentv1.PublicationReferenceCommand {
	return &attachmentv1.PublicationReferenceCommand{
		MessageId: job.ID, DocumentId: job.DocumentID, OwnerId: job.OwnerID,
		Generation: job.Generation, AttachmentIds: append([]string(nil), job.AttachmentIDs...),
	}
}

func (c *Attachment) Stage(ctx context.Context, job domain.PublicationReferenceJob) error {
	if c == nil || c.client == nil {
		return errors.New("stage Attachment references: client is nil")
	}
	return c.client.StagePublicationReferences(coreauth.WithServiceToken(ctx, c.serviceToken), c.command(job))
}

func (c *Attachment) Finalize(ctx context.Context, job domain.PublicationReferenceJob) error {
	if c == nil || c.client == nil {
		return errors.New("finalize Attachment references: client is nil")
	}
	return c.client.FinalizePublicationReferences(coreauth.WithServiceToken(ctx, c.serviceToken), c.command(job))
}

func (c *Attachment) Clear(ctx context.Context, job domain.PublicationReferenceJob) error {
	if c == nil || c.client == nil {
		return errors.New("clear Attachment references: client is nil")
	}
	return c.client.ClearPublicationReferences(coreauth.WithServiceToken(ctx, c.serviceToken), c.command(job))
}

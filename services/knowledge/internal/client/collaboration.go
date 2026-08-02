package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
)

type Collaboration struct {
	baseURL *url.URL
	client  *http.Client
}

func NewCollaboration(options config.CollaborationOptions) (*Collaboration, error) {
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create Collaboration client: %w", err)
	}
	baseURL, err := url.Parse(options.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Collaboration URL: %w", err)
	}
	tlsConfig, err := options.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("create Collaboration client TLS: %w", err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &Collaboration{
		baseURL: baseURL,
		client:  &http.Client{Transport: transport, Timeout: options.RequestTimeout},
	}, nil
}

func (c *Collaboration) PurgeDocument(ctx context.Context, documentID string) error {
	if c == nil || c.client == nil || c.baseURL == nil {
		return errors.New("purge Collaboration document: client is nil")
	}
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return fmt.Errorf("purge Collaboration document: %w", err)
	}
	endpoint := *c.baseURL
	endpoint.Path = "/internal/v1/documents/" + url.PathEscape(documentID)
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create Collaboration purge request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Collaboration purge: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("request Collaboration purge: unexpected status %d", response.StatusCode)
	}
	return nil
}

func (c *Collaboration) Ping(ctx context.Context) error {
	if c == nil || c.client == nil || c.baseURL == nil {
		return errors.New("ping Collaboration: client is nil")
	}
	endpoint := *c.baseURL
	endpoint.Path = "/health/ready"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create Collaboration readiness request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Collaboration readiness: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("request Collaboration readiness: unexpected status %d", response.StatusCode)
	}
	return nil
}

func (c *Collaboration) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	if transport, ok := c.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	return nil
}

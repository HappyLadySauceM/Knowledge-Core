package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const maximumCollaborationResponseBytes = 20 << 20

type Collaboration struct {
	baseURL *url.URL
	client  *http.Client
}

type CollaborationError struct {
	Status int
	Code   int32
	Key    string
}

func (e *CollaborationError) Error() string {
	return fmt.Sprintf("Collaboration request failed with status %d", e.Status)
}

type VersionUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

type Version struct {
	ID         string      `json:"id"`
	DocumentID string      `json:"document_id"`
	Sequence   int64       `json:"sequence"`
	Kind       string      `json:"kind"`
	Label      *string     `json:"label,omitempty"`
	CreatedBy  VersionUser `json:"created_by"`
	CreatedAt  string      `json:"created_at"`
}

type VersionPageInfo struct {
	NextCursor *string `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

type VersionPage struct {
	Items []Version       `json:"items"`
	Page  VersionPageInfo `json:"page"`
}

type VersionDetail struct {
	Version   Version        `json:"version"`
	Content   map[string]any `json:"content"`
	PlainText string         `json:"plain_text"`
}

type collaborationProblem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	Code      int32  `json:"code"`
	Key       string `json:"key"`
	RequestID string `json:"request_id,omitempty"`
	TraceID   string `json:"trace_id,omitempty"`
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

func (c *Collaboration) Ping(ctx context.Context) error {
	var response struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := c.do(ctx, http.MethodGet, "/health/ready", "", nil, http.StatusOK, &response); err != nil {
		return err
	}
	if response.Status != "ready" || response.Service != "collaboration" {
		return errors.New("ping Collaboration: service is not ready")
	}
	return nil
}

func (c *Collaboration) ListVersions(
	ctx context.Context,
	documentID, token, cursor string,
	limit int32,
) (*VersionPage, error) {
	query := make(url.Values)
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.FormatInt(int64(limit), 10))
	}
	path := "/internal/v1/documents/" + url.PathEscape(documentID) + "/versions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var result VersionPage
	if err := c.do(ctx, http.MethodGet, path, token, nil, http.StatusOK, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Collaboration) CreateVersion(
	ctx context.Context,
	documentID, token, label, idempotencyKey string,
) (*Version, error) {
	body := struct {
		Label *string `json:"label,omitempty"`
	}{}
	if label != "" {
		body.Label = &label
	}
	var result Version
	path := "/internal/v1/documents/" + url.PathEscape(documentID) + "/versions"
	if err := c.doWithIdempotency(ctx, http.MethodPost, path, token, idempotencyKey, body, http.StatusCreated, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Collaboration) GetVersion(
	ctx context.Context,
	documentID, versionID, token string,
) (*VersionDetail, error) {
	path := "/internal/v1/documents/" + url.PathEscape(documentID) + "/versions/" + url.PathEscape(versionID)
	var result VersionDetail
	if err := c.do(ctx, http.MethodGet, path, token, nil, http.StatusOK, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Collaboration) RestoreVersion(
	ctx context.Context,
	documentID, versionID, token string,
	expectedSequence int64,
	idempotencyKey string,
) (*Version, error) {
	body := struct {
		ExpectedSequence int64 `json:"expected_sequence"`
	}{ExpectedSequence: expectedSequence}
	path := "/internal/v1/documents/" + url.PathEscape(documentID) + "/versions/" +
		url.PathEscape(versionID) + "/restorations"
	var result Version
	if err := c.doWithIdempotency(ctx, http.MethodPost, path, token, idempotencyKey, body, http.StatusCreated, &result); err != nil {
		return nil, err
	}
	return &result, nil
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

func (c *Collaboration) doWithIdempotency(
	ctx context.Context,
	method, path, token, idempotencyKey string,
	body any,
	expectedStatus int,
	target any,
) error {
	headers := make(http.Header)
	if idempotencyKey != "" {
		headers.Set("Idempotency-Key", idempotencyKey)
	}
	return c.doRequest(ctx, method, path, token, headers, body, expectedStatus, target)
}

func (c *Collaboration) do(
	ctx context.Context,
	method, path, token string,
	body any,
	expectedStatus int,
	target any,
) error {
	return c.doRequest(ctx, method, path, token, nil, body, expectedStatus, target)
}

func (c *Collaboration) doRequest(
	ctx context.Context,
	method, path, token string,
	headers http.Header,
	body any,
	expectedStatus int,
	target any,
) error {
	if c == nil || c.client == nil || c.baseURL == nil {
		return errors.New("request Collaboration: client is nil")
	}
	endpoint, err := c.baseURL.Parse(path)
	if err != nil {
		return fmt.Errorf("resolve Collaboration request URL: %w", err)
	}
	var payload []byte
	if body != nil {
		payload, err = jsoncodec.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Collaboration request: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create Collaboration request: %w", err)
	}
	request.Header.Set("Accept", "application/json, application/problem+json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if requestID := metadata.RequestID(ctx); requestID != "" {
		request.Header.Set(coretrace.RequestIDHeader, requestID)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("request Collaboration: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	responsePayload, err := readBounded(response.Body, maximumCollaborationResponseBytes)
	if err != nil {
		return fmt.Errorf("read Collaboration response: %w", err)
	}
	if response.StatusCode != expectedStatus {
		mediaType, _, mediaTypeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
		expectedMediaType, _, expectedMediaTypeErr := mime.ParseMediaType(apperror.ProblemContentType)
		if mediaTypeErr != nil || expectedMediaTypeErr != nil || mediaType != expectedMediaType {
			return fmt.Errorf("request Collaboration: unexpected status %d", response.StatusCode)
		}
		var problem collaborationProblem
		if decodeErr := jsoncodec.Unmarshal(responsePayload, &problem); decodeErr != nil ||
			problem.Status != response.StatusCode || problem.Code <= 0 || strings.TrimSpace(problem.Key) == "" {
			return fmt.Errorf("request Collaboration: unexpected status %d", response.StatusCode)
		}
		return &CollaborationError{Status: response.StatusCode, Code: problem.Code, Key: problem.Key}
	}
	if target == nil {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errors.New("decode Collaboration response: expected application/json")
	}
	if err := jsoncodec.Unmarshal(responsePayload, target); err != nil {
		return fmt.Errorf("decode Collaboration response: %w", err)
	}
	return nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	limited := io.LimitReader(reader, maximum+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maximum {
		return nil, errors.New("response exceeds size limit")
	}
	return payload, nil
}

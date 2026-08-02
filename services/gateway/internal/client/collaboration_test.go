package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
)

func TestCollaborationClientPropagatesHeadersAndDecodesVersion(t *testing.T) {
	var authorization, idempotency, requestID string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		idempotency = request.Header.Get("Idempotency-Key")
		requestID = request.Header.Get(coretrace.RequestIDHeader)
		response.Header().Set("Content-Type", "application/json; charset=utf-8")
		response.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(response, `{"id":"0198f0e0-7b6d-7a11-8e21-0123456789ab","document_id":"0198f0e0-7b6d-7a11-8e21-1123456789ab","sequence":4,"kind":"manual","created_by":{"id":7,"username":"alice","avatar":""},"created_at":"2026-08-02T12:00:00Z"}`)
	}))
	defer server.Close()

	client, err := NewCollaboration(config.CollaborationOptions{BaseURL: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Collaboration client: %v", err)
		}
	})
	ctx := metadata.WithRequestID(context.Background(), "req_0123456789abcdef")
	version, err := client.CreateVersion(ctx, "0198f0e0-7b6d-7a11-8e21-1123456789ab", "signed-token", "checkpoint", "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	if version == nil || version.Sequence != 4 || authorization != "Bearer signed-token" || idempotency != "idem-1" || requestID != "req_0123456789abcdef" {
		t.Fatalf("version = %#v, authorization = %q, idempotency = %q, requestID = %q", version, authorization, idempotency, requestID)
	}
}

func TestCollaborationClientAcceptsOnlyValidProblemResponses(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantTyped   bool
	}{
		{name: "valid", contentType: "application/problem+json; charset=utf-8", body: `{"type":"urn:knowledge-core:problem:collaboration.not_found","title":"Not Found","status":404,"detail":"version not found","code":40004,"key":"collaboration.not_found"}`, wantTyped: true},
		{name: "wrong content type", contentType: "application/json", body: `{"status":404,"code":40004,"key":"collaboration.not_found"}`},
		{name: "status mismatch", contentType: "application/problem+json", body: `{"status":400,"code":40004,"key":"collaboration.not_found"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				response.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			client, err := NewCollaboration(config.CollaborationOptions{BaseURL: server.URL, RequestTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.GetVersion(context.Background(), "document", "version", "token")
			var typed *CollaborationError
			if errors.As(err, &typed) != test.wantTyped {
				t.Fatalf("error = %v, typed = %v", err, errors.As(err, &typed))
			}
		})
	}
}

func TestCollaborationClientRejectsInvalidSuccessContentTypeAndOversizedResponse(t *testing.T) {
	baseURL, _ := url.Parse("http://collaboration.internal")
	client := &Collaboration{
		baseURL: baseURL,
		client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader(`{"status":"ready","service":"collaboration"}`)),
			}, nil
		})},
	}
	if err := client.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "expected application/json") {
		t.Fatalf("Ping() error = %v", err)
	}

	oversized := strings.NewReader(strings.Repeat("x", maximumCollaborationResponseBytes+1))
	if _, err := readBounded(oversized, maximumCollaborationResponseBytes); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("readBounded() error = %v", err)
	}
}

func TestCollaborationClientHonorsTimeout(t *testing.T) {
	baseURL, _ := url.Parse("http://collaboration.internal")
	client := &Collaboration{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 10 * time.Millisecond,
			Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
		},
	}
	if err := client.Ping(context.Background()); err == nil {
		t.Fatal("Ping() succeeded after its timeout")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

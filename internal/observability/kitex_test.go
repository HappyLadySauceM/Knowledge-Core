package observability_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/bytedance/gopkg/cloud/metainfo"
	"github.com/cloudwego/kitex/pkg/endpoint"
)

func TestKitexMiddlewaresPropagateRequestID(t *testing.T) {
	runtime, err := observability.New(context.Background(), observability.Config{
		Service: "gateway", Environment: "test", Level: "info", Output: &bytes.Buffer{}, SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client := observability.KitexClientMiddleware(runtime)(func(ctx context.Context, _, _ interface{}) error {
		requestID, exists := metainfo.GetPersistentValue(ctx, "x-request-id")
		if !exists || requestID != "request-1" {
			t.Fatalf("propagated request ID = %q, exists = %t", requestID, exists)
		}
		server := observability.KitexServerMiddleware(runtime)(func(serverCtx context.Context, _, _ interface{}) error {
			if requestID := observability.RequestID(serverCtx); requestID != "request-1" {
				t.Fatalf("server request ID = %q", requestID)
			}
			return nil
		})
		return server(ctx, nil, nil)
	})
	ctx := observability.WithRequestID(context.Background(), "request-1")
	if err := client(ctx, nil, nil); err != nil {
		t.Fatalf("middleware error = %v", err)
	}
}

func TestKitexServerCreatesRequestIDWhenMissing(t *testing.T) {
	runtime, err := observability.New(context.Background(), observability.Config{
		Service: "identity", Environment: "test", Level: "info", Output: &bytes.Buffer{}, SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	middleware := observability.KitexServerMiddleware(runtime)(endpoint.Endpoint(func(ctx context.Context, _, _ interface{}) error {
		if observability.RequestID(ctx) == "" {
			t.Fatal("server did not create a request ID")
		}
		return nil
	}))
	if err := middleware(context.Background(), nil, nil); err != nil {
		t.Fatalf("middleware error = %v", err)
	}
}

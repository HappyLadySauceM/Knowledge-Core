package http

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestHealthHandler(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)

	request := app.NewContext(0)
	healthHandler(registry.Ready)(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusOK {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
	var response statusResponse
	if err := jsoncodec.Unmarshal(request.Response.Body(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if response.Status != "ok" {
		t.Fatalf("response = %#v", response)
	}

	registry.SetServing(false)
	request = app.NewContext(0)
	healthHandler(registry.Ready)(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusServiceUnavailable {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
}

func TestRecoveryPreservesAccessLogAndRecordsStack(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	request := app.NewContext(0)
	request.SetHandlers(app.HandlersChain{
		accessLogMiddleware(logger),
		recoveryMiddleware(logger),
		func(context.Context, *app.RequestContext) { panic("boom") },
	})

	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
	logs := output.String()
	for _, expected := range []string{"HTTP handler panic recovered", "HTTP request completed", "boom", "stack="} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("logs do not contain %q: %s", expected, logs)
		}
	}
}

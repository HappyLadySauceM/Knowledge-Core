package hertz

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	corelog "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestHealthHandler(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	request := app.NewContext(0)
	healthHandler(registry.Ready)(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusOK || !bytes.Contains(request.Response.Body(), []byte(`"status":"ok"`)) {
		t.Fatalf("response = %d %s", request.Response.StatusCode(), request.Response.Body())
	}

	if err := registry.AddReadiness("broken", func(context.Context) error { return errors.New("down") }); err != nil {
		t.Fatal(err)
	}
	request = app.NewContext(0)
	healthHandler(registry.Ready)(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusServiceUnavailable {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
}

func TestAccessLogSuppressesOnlySuccessfulHealthRequests(t *testing.T) {
	var output bytes.Buffer
	server := &AdminServer{logComponent: "test.admin"}
	logger := slog.New(slog.NewTextHandler(&output, nil))
	control := corelog.NewRequestControl(false)
	request := app.NewContext(0)
	request.Request.SetRequestURI("/livez")
	request.SetFullPath("/livez")
	request.SetHandlers(app.HandlersChain{server.accessLogMiddleware(logger, control), func(context.Context, *app.RequestContext) { request.Status(consts.StatusOK) }})
	request.Next(context.Background())
	if output.Len() != 0 {
		t.Fatalf("successful health log was not suppressed: %s", output.String())
	}
	request = app.NewContext(0)
	request.Request.SetRequestURI("/readyz")
	request.SetFullPath("/readyz")
	request.SetHandlers(app.HandlersChain{server.accessLogMiddleware(logger, control), func(context.Context, *app.RequestContext) { request.Status(consts.StatusServiceUnavailable) }})
	request.Next(context.Background())
	if !strings.Contains(output.String(), "http.status_code=503") {
		t.Fatalf("failed health log was suppressed: %s", output.String())
	}
}

func TestRecoveryMiddlewareRecordsStack(t *testing.T) {
	var output bytes.Buffer
	server := &AdminServer{logComponent: "test.admin"}
	request := app.NewContext(0)
	request.SetHandlers(app.HandlersChain{
		server.recoveryMiddleware(slog.New(slog.NewTextHandler(&output, nil))),
		func(context.Context, *app.RequestContext) { panic("boom") },
	})
	request.Next(context.Background())
	if request.Response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
	logs := output.String()
	for _, expected := range []string{"HTTP handler panic recovered", "boom", "stack="} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("logs do not contain %q: %s", expected, logs)
		}
	}
}

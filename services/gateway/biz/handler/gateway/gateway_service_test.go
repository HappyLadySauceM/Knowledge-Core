package gateway_test

import (
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/health"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/router"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestHealthRoutes(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	engine := server.New()
	engine.Use(middleware.RequestID(), middleware.Dependencies(registry))
	router.GeneratedRegister(engine)

	live := ut.PerformRequest(engine.Engine, "GET", "/health/live", nil)
	if live.Code != 200 || !strings.Contains(live.Body.String(), `"status":"live"`) {
		t.Fatalf("live response = %d %s", live.Code, live.Body.String())
	}
	ready := ut.PerformRequest(engine.Engine, "GET", "/health/ready", nil)
	if ready.Code != 200 || !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready response = %d %s", ready.Code, ready.Body.String())
	}

	registry.SetServing(false)
	notReady := ut.PerformRequest(engine.Engine, "GET", "/health/ready", nil)
	if notReady.Code != 503 || !strings.Contains(notReady.Body.String(), `"code":10001`) {
		t.Fatalf("not-ready response = %d %s", notReady.Code, notReady.Body.String())
	}
}

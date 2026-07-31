package metrics_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metrics"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestHertzMiddlewareUsesRouteTemplatesAndIgnoresMetrics(t *testing.T) {
	registry := newRegistry(t, "http")
	engine := server.New()
	engine.Use(metrics.HertzServerMiddleware(registry, func(_ context.Context, request *app.RequestContext) bool {
		return string(request.Request.URI().Path()) == "/metrics"
	}))
	engine.GET("/documents/:id", func(_ context.Context, request *app.RequestContext) {
		request.String(201, "created")
	})
	engine.GET("/metrics", func(_ context.Context, request *app.RequestContext) {
		request.String(200, "ignored")
	})

	response := ut.PerformRequest(engine.Engine, "GET", "/documents/42?secret=must-not-appear", nil)
	if response.Code != 201 {
		t.Fatalf("response status = %d", response.Code)
	}
	ut.PerformRequest(engine.Engine, "GET", "/metrics", nil)

	family := gatherFamily(t, registry, "knowledge_core_http_server_requests_total")
	if got := counterValue(family, map[string]string{
		"method":      "GET",
		"route":       "/documents/:id",
		"status_code": "201",
	}); got != 1 {
		t.Fatalf("HTTP request counter = %v, want 1", got)
	}
	if got := counterValue(family, map[string]string{
		"method":      "GET",
		"route":       "/metrics",
		"status_code": "200",
	}); got != 0 {
		t.Fatalf("ignored metrics request counter = %v, want 0", got)
	}
}

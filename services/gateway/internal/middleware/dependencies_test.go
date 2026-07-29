package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/observability"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestAccessLogUsesRouteAndDoesNotLogRequestBody(t *testing.T) {
	var output bytes.Buffer
	runtime, err := observability.New(context.Background(), observability.Config{
		Service: "gateway", Environment: "test", Level: "info", Output: &output, SampleRatio: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	engine := server.New()
	engine.Use(middleware.RequestID(), middleware.AccessLog(runtime.Logger()))
	engine.POST("/documents/:id", func(_ context.Context, c *app.RequestContext) { c.String(200, "ok") })
	body := `{"password":"must-not-appear"}`
	response := ut.PerformRequest(engine.Engine, "POST", "/documents/42?token=must-not-appear", &ut.Body{
		Body: strings.NewReader(body), Len: len(body),
	})
	if response.Code != 200 {
		t.Fatalf("response code = %d", response.Code)
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("Unmarshal() error = %v; output = %q", err, output.String())
	}
	if record["http_route"] != "/documents/:id" || record["request_id"] == "" {
		t.Fatalf("access record = %#v", record)
	}
	if strings.Contains(output.String(), "must-not-appear") {
		t.Fatalf("access log leaked request data: %s", output.String())
	}
}

func TestRequireAuthenticatedStopsHandler(t *testing.T) {
	engine := server.New()
	called := false
	engine.Use(middleware.RequestID())
	engine.GET("/protected", middleware.RequireAuthenticated(), func(_ context.Context, _ *app.RequestContext) {
		called = true
	})
	response := ut.PerformRequest(engine.Engine, "GET", "/protected", nil)
	if response.Code != 401 || called || !strings.Contains(response.Body.String(), `"code":10003`) {
		t.Fatalf("response = %d %s, called = %t", response.Code, response.Body.String(), called)
	}
}

func TestRequireRolesEnforcesAuthenticationAndRole(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		principal  auth.Principal
		verifyErr  error
		wantStatus int
		wantCalled bool
	}{
		{name: "missing token", wantStatus: 401},
		{name: "invalid token", header: "Bearer invalid", verifyErr: errors.New("invalid"), wantStatus: 401},
		{name: "wrong role", header: "Bearer user", principal: auth.Principal{UserID: 1, Role: "user"}, wantStatus: 403},
		{name: "allowed role", header: "Bearer admin", principal: auth.Principal{UserID: 1, Role: "admin"}, wantStatus: 200, wantCalled: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := server.New()
			called := false
			engine.Use(middleware.RequestID(), middleware.Authentication(fakeVerifier{principal: test.principal, err: test.verifyErr}))
			engine.GET("/admin", middleware.RequireRoles("admin"), func(_ context.Context, c *app.RequestContext) {
				called = true
				c.Status(200)
			})
			headers := []ut.Header(nil)
			if test.header != "" {
				headers = append(headers, ut.Header{Key: "Authorization", Value: test.header})
			}
			response := ut.PerformRequest(engine.Engine, "GET", "/admin", nil, headers...)
			if response.Code != test.wantStatus || called != test.wantCalled {
				t.Fatalf("response = %d %s, called = %t", response.Code, response.Body.String(), called)
			}
		})
	}
}

func TestRequestIDReplacesUnsafeValue(t *testing.T) {
	engine := server.New()
	engine.Use(middleware.RequestID())
	engine.GET("/", func(_ context.Context, c *app.RequestContext) { c.Status(204) })
	response := ut.PerformRequest(engine.Engine, "GET", "/", nil, ut.Header{Key: "X-Request-ID", Value: "unsafe request id"})
	requestID := string(response.Header().Peek("X-Request-ID"))
	if requestID == "" || requestID == "unsafe request id" {
		t.Fatalf("response request ID = %q", requestID)
	}
}

type fakeVerifier struct {
	principal auth.Principal
	err       error
}

func (v fakeVerifier) Verify(string) (auth.Principal, error) {
	return v.principal, v.err
}

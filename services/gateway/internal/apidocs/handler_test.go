package apidocs

import (
	"context"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

func TestRegisterRequiresServer(t *testing.T) {
	if err := Register(nil, true); err == nil {
		t.Fatal("Register(nil, true) unexpectedly succeeded")
	}
}

func TestRegisterDisabledDoesNotAddRoutes(t *testing.T) {
	h := server.New()
	if err := Register(h, false); err != nil {
		t.Fatal(err)
	}
	if got := len(h.Routes()); got != 0 {
		t.Fatalf("route count = %d, want 0", got)
	}
}

func TestRegisterEnabledAddsOnlyDocumentationRoutes(t *testing.T) {
	h := server.New()
	if err := Register(h, true); err != nil {
		t.Fatal(err)
	}
	if got := len(h.Routes()); got != 9 {
		t.Fatalf("route count = %d, want 9", got)
	}
}

func TestServeAssetSetsSecurityHeaders(t *testing.T) {
	request := app.NewContext(0)
	serveAsset("index.html", "text/html; charset=utf-8")(context.Background(), request)

	if got := request.Response.StatusCode(); got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if len(request.Response.Body()) == 0 {
		t.Fatal("documentation response body is empty")
	}
	for key, want := range map[string]string{
		"Content-Security-Policy": documentationCSP,
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := string(request.Response.Header.Peek(key)); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

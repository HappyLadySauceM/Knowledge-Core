package kitex_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	platformkitex "github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/transport/kitex"
)

func TestPing(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	response, err := platformkitex.NewHandler(registry).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "platform" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

func TestPingReportsNotReadyWhileDraining(t *testing.T) {
	response, err := platformkitex.NewHandler(health.NewRegistry()).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Status != "not_ready" {
		t.Fatalf("Ping() status = %q", response.Status)
	}
}

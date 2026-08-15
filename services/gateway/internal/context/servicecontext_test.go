package context

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
)

func TestGatewayReadinessIgnoresUpstreamRPC(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	if err := addReadinessChecks(registry, time.Second, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("addReadinessChecks() error = %v", err)
	}
	if err := registry.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v, want nil when redis is healthy", err)
	}
}

func TestGatewayReadinessFailsWhenRedisFails(t *testing.T) {
	redisErr := errors.New("redis unavailable")
	registry := health.NewRegistry()
	registry.SetServing(true)
	if err := addReadinessChecks(registry, time.Second, func(context.Context) error { return redisErr }); err != nil {
		t.Fatalf("addReadinessChecks() error = %v", err)
	}
	err := registry.Ready(context.Background())
	if !errors.Is(err, redisErr) {
		t.Fatalf("Ready() error = %v, want %v", err, redisErr)
	}
	if strings.Contains(errString(err), "identity") || strings.Contains(errString(err), "collaboration") {
		t.Fatalf("Ready() still depends on peer RPC: %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

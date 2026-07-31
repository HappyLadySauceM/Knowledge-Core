package redis

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	redisclient "github.com/redis/go-redis/v9"
)

func TestOpenRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	opts := *option.NewRedisOptions()
	opts.Address = ""
	if _, err := Open(context.Background(), opts, nil); err == nil || !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("Open() error = %v, want invalid options", err)
	}
}

func TestOpenClosesClientAfterPingFailure(t *testing.T) {
	t.Parallel()
	opts := *option.NewRedisOptions()
	opts.Address = "127.0.0.1:1"
	opts.DialTimeout = 25 * time.Millisecond
	opts.ReadTimeout = 25 * time.Millisecond
	opts.WriteTimeout = 25 * time.Millisecond
	opts.MaxRetries = -1
	if _, err := Open(context.Background(), opts, nil); err == nil || !strings.Contains(err.Error(), "ping redis") {
		t.Fatalf("Open() error = %v, want ping redis failure", err)
	}
}

func TestNilResourceLifecycleIsSafe(t *testing.T) {
	t.Parallel()
	var resource *Resource
	if err := resource.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := resource.Ping(context.Background()); err == nil {
		t.Fatal("Ping() error = nil, want nil client error")
	}
}

func TestPingRejectsNilContext(t *testing.T) {
	t.Parallel()
	resource := &Resource{Client: redisclient.NewClient(&redisclient.Options{
		Addr: "127.0.0.1:1",
	})}
	t.Cleanup(func() { _ = resource.Close() })

	if err := resource.Ping(nil); err == nil || !strings.Contains(err.Error(), "context is required") { //nolint:staticcheck // Explicitly verifies the nil-context contract.
		t.Fatalf("Ping() error = %v, want context is required", err)
	}
}

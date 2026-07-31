package etcd

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestNewEtcdClientRejectsInvalidTLSBeforeCreation(t *testing.T) {
	t.Parallel()
	opts := *option.NewEtcdOptions()
	opts.TLS = option.TLSOptions{Enabled: true, CAFile: "missing-ca.pem"}
	_, err := newEtcdClient(opts)
	if err == nil || !strings.Contains(err.Error(), "read TLS CA file") {
		t.Fatalf("newEtcdClient() error = %v, want fail-closed TLS error", err)
	}
}

func TestResourcesSharesAndClosesOwnedClient(t *testing.T) {
	t.Parallel()
	opts := *option.NewEtcdOptions()
	opts.Endpoints = []string{"127.0.0.1:1"}
	opts.DialTimeout = 25 * time.Millisecond

	client, err := newEtcdClient(opts)
	if err != nil {
		t.Fatalf("newEtcdClient() error = %v", err)
	}
	resources, err := newResources(client, opts)
	if err != nil {
		_ = client.Close()
		t.Fatalf("newResources() error = %v", err)
	}
	registryClient, ok := resources.registry.client.(*clientv3.Client)
	if !ok || registryClient != resources.HealthClient {
		t.Fatalf("registry client = %T %p, health client = %p; want the same owned client", resources.registry.client, registryClient, resources.HealthClient)
	}

	if err := resources.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-client.Ctx().Done():
	default:
		t.Fatal("Close() did not close the shared etcd client")
	}
	if err := resources.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestOpenRejectsInvalidOptionsWithoutDialing(t *testing.T) {
	t.Parallel()
	opts := *option.NewEtcdOptions()
	opts.Endpoints = nil
	_, err := Open(context.Background(), opts, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid options") {
		t.Fatalf("Open() error = %v, want invalid options", err)
	}
}

func TestOpenReportsHealthFailure(t *testing.T) {
	t.Parallel()
	opts := *option.NewEtcdOptions()
	opts.Endpoints = []string{"127.0.0.1:1"}
	opts.DialTimeout = 25 * time.Millisecond
	opts.RequestTimeout = 50 * time.Millisecond
	_, err := Open(context.Background(), opts, nil)
	if err == nil || !strings.Contains(err.Error(), "ping etcd") {
		t.Fatalf("Open() error = %v, want ping etcd failure", err)
	}
}

func TestNilResourcesLifecycleIsSafe(t *testing.T) {
	t.Parallel()
	var resources *Resources
	if err := resources.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := resources.Ping(context.Background()); err == nil {
		t.Fatal("Ping() error = nil, want nil client error")
	}
}

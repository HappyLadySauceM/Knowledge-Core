package option

import (
	"strings"
	"testing"
)

func TestKitexClientOptionsRequireDialAddress(t *testing.T) {
	options := NewKitexClientOptions()
	if err := options.Validate(); err == nil || !strings.Contains(err.Error(), "rpc_client.address") {
		t.Fatalf("Validate() error = %v, want required address", err)
	}

	options.Address = "identity:8881"
	if err := options.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	options.Address = "identity"
	if err := options.Validate(); err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("Validate() error = %v, want host:port", err)
	}

	options.Address = "identity:0"
	if err := options.Validate(); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("Validate() error = %v, want non-zero port", err)
	}
}

func TestRejectLoopbackEndpoint(t *testing.T) {
	if err := RejectLoopbackEndpoint("identity_rpc.address", "knowledge-core-identity.knowledge-core-dev.svc.cluster.local:8881"); err != nil {
		t.Fatalf("RejectLoopbackEndpoint() error = %v", err)
	}
	for _, address := range []string{"127.0.0.1:8881", "localhost:8881", "[::1]:8881"} {
		if err := RejectLoopbackEndpoint("identity_rpc.address", address); err == nil {
			t.Fatalf("RejectLoopbackEndpoint(%q) accepted a loopback address", address)
		}
	}
}

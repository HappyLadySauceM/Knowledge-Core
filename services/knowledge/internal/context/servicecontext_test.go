package context

import (
	"context"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/scanner"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/storage"
)

func TestKnowledgeReadinessOmitsPeerRPC(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	cfg := config.New()
	if err := addReadinessChecks(registry, cfg, nil, nil, &storage.S3{}, &scanner.ClamAV{}); err != nil {
		t.Fatalf("addReadinessChecks() error = %v", err)
	}
	err := registry.Ready(context.Background())
	if err == nil {
		t.Fatal("Ready() succeeded with nil local dependencies")
	}
	message := err.Error()
	for _, peer := range []string{"identity", "collaboration", "Identity", "Collaboration"} {
		if strings.Contains(message, peer) {
			t.Fatalf("Ready() still pings peer RPC %q: %v", peer, err)
		}
	}
}

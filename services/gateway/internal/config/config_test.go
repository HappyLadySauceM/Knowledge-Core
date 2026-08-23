package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/configcenter"
	"github.com/spf13/cobra"
)

func TestConfigValidate(t *testing.T) {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cfg := New()
	cfg.Auth.PublicKey = keys.PublicKey
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cfg.AdminHTTP.Address = cfg.PublicHTTP.Address
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRateLimitRequiresMillisecondWindow(t *testing.T) {
	options := NewRateLimitOptions()
	options.Window = time.Nanosecond
	if err := options.Validate(); err == nil {
		t.Fatal("Validate() accepted a sub-millisecond rate-limit window")
	}
}

func TestProviderLoadsPublicKeyFromEnvironment(t *testing.T) {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_AUTH_PUBLIC_KEY", keys.PublicKey)
	provider := NewProvider()
	provider.configFile = filepath.Join("..", "..", "etc", "config.yaml")
	loaded, err := provider.Load(context.Background(), &cobra.Command{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Auth == nil || loaded.Auth.PublicKey != keys.PublicKey {
		t.Fatalf("loaded auth = %#v", loaded.Auth)
	}
}

func TestApplicationDocumentRespectsEnvironmentAndRejectsSecrets(t *testing.T) {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_AUTH_PUBLIC_KEY", keys.PublicKey)
	t.Setenv("GATEWAY_LOG_LEVEL", "error")
	current := New()
	current.Auth.PublicKey = keys.PublicKey
	document := configcenter.DynamicDocument{APIVersion: configcenter.ApplicationAPIVersion, Kind: configcenter.ApplicationKind, Service: "gateway", Revision: 2, Config: map[string]any{"log": map[string]any{"level": "debug", "health_check_requests": false}, "rate_limit": map[string]any{"global_limit": 500}}}
	candidate, result, err := applyDocument(current, current, current, document)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Log.Level != "error" || candidate.Log.HealthCheckRequests || candidate.RateLimit.GlobalLimit != 500 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if len(result.RestartRequiredFields) != 0 {
		t.Fatalf("restart fields = %#v", result.RestartRequiredFields)
	}
	document.Config = map[string]any{"auth": map[string]any{"public_key": "secret"}}
	if _, _, err := applyDocument(current, current, current, document); err == nil {
		t.Fatal("sensitive field was accepted")
	}
}

func TestApplicationDocumentRebuildsFromBaseline(t *testing.T) {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	baseline := New()
	baseline.Auth.PublicKey = keys.PublicKey
	first := configcenter.DynamicDocument{APIVersion: configcenter.ApplicationAPIVersion, Kind: configcenter.ApplicationKind, Service: "gateway", Revision: 2, Config: map[string]any{"rate_limit": map[string]any{"global_limit": 900}}}
	current, _, err := applyDocument(baseline, baseline, baseline, first)
	if err != nil {
		t.Fatal(err)
	}
	second := configcenter.DynamicDocument{APIVersion: configcenter.ApplicationAPIVersion, Kind: configcenter.ApplicationKind, Service: "gateway", Revision: 3, Config: map[string]any{"log": map[string]any{"level": "debug"}}}
	candidate, _, err := applyDocument(current, baseline, baseline, second)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.RateLimit.GlobalLimit != baseline.RateLimit.GlobalLimit {
		t.Fatalf("global limit = %d, want baseline %d", candidate.RateLimit.GlobalLimit, baseline.RateLimit.GlobalLimit)
	}
	if baseline.Log.Level != "info" || baseline.RateLimit.GlobalLimit != 300 {
		t.Fatalf("baseline was mutated: %#v", baseline)
	}
}

func TestCORSRejectsWildcardAndInvalidProxy(t *testing.T) {
	options := CORSOptions{
		AllowedOrigins:    []string{"https://*.example.com"},
		TrustedProxyCIDRs: []string{"not-a-cidr"},
	}
	if err := options.Validate(); err == nil {
		t.Fatal("Validate() accepted unsafe CORS options")
	}
}

func TestProductionCollaborationRequiresMutualTLS(t *testing.T) {
	keys, err := coreauth.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	cfg := New()
	cfg.Auth.PublicKey = keys.PublicKey
	cfg.App.Environment = "production"
	cfg.Endpoints.CollaborationWebSocketBaseURL = "wss://collaboration.example.com"
	cfg.PublicHTTP.TLS.Enabled = true
	cfg.PublicHTTP.TLS.CertFile = "server.crt"
	cfg.PublicHTTP.TLS.KeyFile = "server.key"
	cfg.Endpoints.PublicBaseURL = "https://api.example.com"
	cfg.CollaborationRPC.TLS.Enabled = true
	cfg.AttachmentRPC.TLS.Enabled = true

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires a CA and client certificate") {
		t.Fatalf("Validate() error = %v", err)
	}

	cfg.CollaborationRPC.TLS.CAFile = "ca.pem"
	cfg.CollaborationRPC.TLS.CertFile = "client.pem"
	cfg.CollaborationRPC.TLS.KeyFile = "client-key.pem"
	cfg.CollaborationRPC.TLS.InsecureSkipVerify = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be disabled") {
		t.Fatalf("Validate() error = %v", err)
	}

	cfg.CollaborationRPC.TLS.InsecureSkipVerify = false
	cfg.AttachmentRPC.TLS.CAFile = "ca.pem"
	cfg.AttachmentRPC.TLS.CertFile = "client.pem"
	cfg.AttachmentRPC.TLS.KeyFile = "client-key.pem"
	cfg.IdentityRPC.Address = "identity.example.svc:8881"
	cfg.KnowledgeRPC.Address = "knowledge.example.svc:8882"
	cfg.CollaborationRPC.Address = "collaboration.example.svc:8883"
	cfg.AttachmentRPC.Address = "attachment.example.svc:8884"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

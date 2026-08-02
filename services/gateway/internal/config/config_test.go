package config

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
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
	cfg.Endpoints.CollaborationWebSocketURL = "wss://collaboration.example.com/collaboration"
	cfg.PublicHTTP.TLS.Enabled = true
	cfg.PublicHTTP.TLS.CertFile = "server.crt"
	cfg.PublicHTTP.TLS.KeyFile = "server.key"
	cfg.Endpoints.PublicBaseURL = "https://api.example.com"
	cfg.Collaboration.BaseURL = "https://collaboration.internal"
	cfg.Collaboration.TLS.Enabled = true

	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires a CA and client certificate") {
		t.Fatalf("Validate() error = %v", err)
	}

	cfg.Collaboration.TLS.CAFile = "ca.pem"
	cfg.Collaboration.TLS.CertFile = "client.pem"
	cfg.Collaboration.TLS.KeyFile = "client-key.pem"
	cfg.Collaboration.TLS.InsecureSkipVerify = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be disabled") {
		t.Fatalf("Validate() error = %v", err)
	}

	cfg.Collaboration.TLS.InsecureSkipVerify = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

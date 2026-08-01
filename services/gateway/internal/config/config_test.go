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

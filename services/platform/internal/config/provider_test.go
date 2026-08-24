package config

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/spf13/cobra"
)

func TestProviderLoadUsesDeploymentInternalTokenName(t *testing.T) {
	t.Setenv("PLATFORM_AUTH_PUBLIC_KEY", "test-public-key")
	t.Setenv("PLATFORM_INTERNAL_TOKEN", "legacy-deployment-token")
	t.Setenv("PLATFORM_ENCRYPTION_KEY_ID", "test-v1")
	t.Setenv("PLATFORM_ENCRYPTION_KEK", base64.StdEncoding.EncodeToString(make([]byte, 32)))

	provider := &Provider{configFile: "../../etc/config.yaml"}
	loaded, err := provider.Load(context.Background(), &cobra.Command{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Auth.InternalToken != "legacy-deployment-token" {
		t.Fatalf("InternalToken = %q, want deployment Secret value", loaded.Auth.InternalToken)
	}
}

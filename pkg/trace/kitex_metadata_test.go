package trace

import (
	"context"
	"testing"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
)

func TestKitexRPCMetadataPropagatesCredentialsWithoutChangingValues(t *testing.T) {
	ctx := metadata.WithRequestID(context.Background(), "request-123")
	ctx = coreauth.WithAccessToken(ctx, "access-token")
	ctx = coreauth.WithServiceToken(ctx, "service-token")

	injected := injectRPCMetadata(ctx)
	if got := metainfoValue(injected, "knowledge-core-access-token"); got != "access-token" {
		t.Fatalf("injected access token = %q", got)
	}
	if got := metainfoValue(injected, "knowledge-core-service-token"); got != "service-token" {
		t.Fatalf("injected service token = %q", got)
	}

	extracted := extractRPCMetadata(injected)
	if got := coreauth.AccessToken(extracted); got != "access-token" {
		t.Fatalf("extracted access token = %q", got)
	}
	if got := coreauth.ServiceToken(extracted); got != "service-token" {
		t.Fatalf("extracted service token = %q", got)
	}
}

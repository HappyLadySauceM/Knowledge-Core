package trace

import (
	"context"
	"testing"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
)

func TestKitexRPCMetadataPropagatesAccessTokenWithoutChangingValue(t *testing.T) {
	ctx := metadata.WithRequestID(context.Background(), "request-123")
	ctx = coreauth.WithAccessToken(ctx, "access-token")

	injected := injectRPCMetadata(ctx)
	if got := metainfoValue(injected, "knowledge-core-access-token"); got != "access-token" {
		t.Fatalf("injected access token = %q", got)
	}
	extracted := extractRPCMetadata(injected)
	if got := coreauth.AccessToken(extracted); got != "access-token" {
		t.Fatalf("extracted access token = %q", got)
	}
}

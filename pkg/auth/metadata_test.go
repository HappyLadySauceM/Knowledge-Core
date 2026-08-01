package auth

import (
	"context"
	"testing"
)

func TestAccessTokenMetadata(t *testing.T) {
	ctx := WithAccessToken(context.Background(), " token ")
	if got := AccessToken(ctx); got != "token" {
		t.Fatalf("AccessToken() = %q", got)
	}
}

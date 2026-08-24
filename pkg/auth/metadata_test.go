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

func TestServiceTokenMetadata(t *testing.T) {
	ctx := WithServiceToken(context.Background(), " service-token ")
	if got := ServiceToken(ctx); got != "service-token" {
		t.Fatalf("ServiceToken() = %q", got)
	}
}

package metadata_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
)

func TestRequestMetadata(t *testing.T) {
	ctx := metadata.WithRequestID(context.Background(), "request-1")
	ctx = metadata.WithUserID(ctx, 42)
	if got := metadata.RequestID(ctx); got != "request-1" {
		t.Fatalf("RequestID() = %q", got)
	}
	if got := metadata.UserID(ctx); got != 42 {
		t.Fatalf("UserID() = %d", got)
	}
}

func TestWithRequestIDRejectsUnsafeInput(t *testing.T) {
	for _, requestID := range []string{"", "with space", "line\nbreak", string(make([]byte, metadata.MaxRequestIDLength+1))} {
		ctx := metadata.WithRequestID(context.Background(), requestID)
		if got := metadata.RequestID(ctx); got != "" {
			t.Fatalf("RequestID() = %q for unsafe input %q", got, requestID)
		}
	}
}

func TestNewRequestIDIsHeaderSafeAndUnique(t *testing.T) {
	pattern := regexp.MustCompile(`^[0-9a-f]{32}$`)
	seen := make(map[string]struct{}, 128)
	for range 128 {
		requestID := metadata.NewRequestID()
		if !pattern.MatchString(requestID) || !metadata.ValidRequestID(requestID) {
			t.Fatalf("NewRequestID() = %q", requestID)
		}
		if _, exists := seen[requestID]; exists {
			t.Fatalf("duplicate request ID %q", requestID)
		}
		seen[requestID] = struct{}{}
	}
}

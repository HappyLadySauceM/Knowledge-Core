package email

import (
	"testing"
	"time"
)

func TestBackoffIsBounded(t *testing.T) {
	if got := backoff(1); got != 30*time.Second {
		t.Fatalf("first retry delay = %s, want 30s", got)
	}
	if got := backoff(8); got != time.Hour {
		t.Fatalf("last retry delay = %s, want 1h", got)
	}
	if got := backoff(99); got != time.Hour {
		t.Fatalf("bounded retry delay = %s, want 1h", got)
	}
}

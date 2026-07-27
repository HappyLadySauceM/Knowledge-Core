package postgres

import (
	"context"
	"strings"
	"testing"
)

func TestUpRequiresDSN(t *testing.T) {
	err := Up(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "database DSN is required") {
		t.Fatalf("Up() error = %v", err)
	}
}

package postgres_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database"
	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database/postgres"
)

func TestProviderRejectsEmptyDSN(t *testing.T) {
	provider := postgres.NewProvider()
	if provider.Name() != "postgres" {
		t.Fatalf("Name() = %q", provider.Name())
	}
	if _, err := provider.Open(context.Background(), database.Config{}); err == nil {
		t.Fatal("Open() accepted an empty DSN")
	}
}

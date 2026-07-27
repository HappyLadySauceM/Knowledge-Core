package health_test

import (
	"context"
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/health"
)

func TestRegistryRequiresServingAndHealthyDependencies(t *testing.T) {
	registry := health.NewRegistry()
	dependencyErr := errors.New("database unavailable")
	if err := registry.Add("database", func(context.Context) error { return dependencyErr }); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := registry.Ready(context.Background()); err == nil {
		t.Fatal("Ready() succeeded before serving")
	}
	registry.SetServing(true)
	if err := registry.Ready(context.Background()); !errors.Is(err, dependencyErr) {
		t.Fatalf("Ready() error = %v", err)
	}
	registry.SetServing(false)
	if err := registry.Ready(context.Background()); err == nil {
		t.Fatal("Ready() succeeded while draining")
	}
}

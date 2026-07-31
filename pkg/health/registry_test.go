package health_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/health"
)

func TestReadinessRequiresServingAndAllChecks(t *testing.T) {
	registry := health.NewRegistry()
	if err := registry.Add("postgres", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	redisErr := errors.New("redis unavailable")
	if err := registry.AddReadiness("redis", func(context.Context) error { return redisErr }); err != nil {
		t.Fatalf("AddReadiness() error = %v", err)
	}
	if !errors.Is(registry.Ready(context.Background()), health.ErrNotServing) {
		t.Fatal("Ready() did not reject a non-serving service")
	}
	registry.SetServing(true)
	if err := registry.Ready(context.Background()); err == nil || !errors.Is(err, redisErr) {
		t.Fatalf("Ready() error = %v", err)
	}
}

func TestLivenessIsIndependentOfServing(t *testing.T) {
	registry := health.NewRegistry()
	if err := registry.AddLiveness("event-loop", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("AddLiveness() error = %v", err)
	}
	if err := registry.Live(context.Background()); err != nil {
		t.Fatalf("Live() error = %v", err)
	}
}

func TestRegistryRejectsDuplicatesAndContainsPanics(t *testing.T) {
	registry := health.NewRegistry()
	check := func(context.Context) error { return nil }
	if err := registry.Add("postgres", check); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := registry.Add("postgres", check); err == nil {
		t.Fatal("Add() accepted a duplicate")
	}
	if err := registry.AddLiveness("loop", func(context.Context) error { panic("boom") }); err != nil {
		t.Fatalf("AddLiveness() error = %v", err)
	}
	if err := registry.Live(context.Background()); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("Live() panic error = %v", err)
	}
}

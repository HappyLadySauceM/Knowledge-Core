package config_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/config"
)

type source struct {
	name     string
	snapshot config.Snapshot
}

func (s source) Name() string { return s.name }
func (s source) Load(context.Context) (config.Snapshot, error) {
	return s.snapshot, nil
}
func (s source) Close() error { return nil }

func TestLoadUsesLaterSourceAsHigherPriority(t *testing.T) {
	low := source{name: "default", snapshot: config.Snapshot{"address": []byte(":8080"), "level": []byte("info")}}
	high := source{name: "environment", snapshot: config.Snapshot{"address": []byte(":9090")}}

	got, err := config.Load(context.Background(), low, high)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(got["address"]) != ":9090" || string(got["level"]) != "info" {
		t.Fatalf("Load() = %#v", got)
	}

	high.snapshot["address"][0] = 'x'
	if string(got["address"]) != ":9090" {
		t.Fatal("Load() retained mutable source bytes")
	}
}

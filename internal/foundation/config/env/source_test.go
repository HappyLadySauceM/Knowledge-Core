package env_test

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/config/env"
)

func TestSourceOnlyReturnsExplicitValues(t *testing.T) {
	values := map[string]string{"KC_ENV": "test"}
	source := env.NewWithLookup(map[string]string{
		"KC_ENV":       "env",
		"KC_LOG_LEVEL": "log_level",
	}, func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})

	snapshot, err := source.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(snapshot["env"]) != "test" {
		t.Fatalf("env = %q", snapshot["env"])
	}
	if _, exists := snapshot["log_level"]; exists {
		t.Fatal("unset environment value was returned")
	}
}

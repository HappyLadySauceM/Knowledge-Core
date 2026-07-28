package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/command"
)

func TestCommandRunsServiceWithCommandContext(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "expected")
	called := false
	cmd := command.New("gateway", func(runCtx context.Context) error {
		called = true
		if value := runCtx.Value(contextKey{}); value != "expected" {
			t.Fatalf("runner context value = %v", value)
		}
		return nil
	})
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if !called {
		t.Fatal("runner was not called")
	}
}

func TestCommandRejectsArgumentsWithoutPrintingUsage(t *testing.T) {
	cmd := command.New("gateway", func(context.Context) error { return nil })
	var output strings.Builder
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"serve"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("ExecuteContext() succeeded")
	}
	if output.Len() != 0 {
		t.Fatalf("command output = %q", output.String())
	}
}

func TestCommandPropagatesRunnerError(t *testing.T) {
	want := errors.New("startup failed")
	cmd := command.New("identity", func(context.Context) error { return want })
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, want) {
		t.Fatalf("ExecuteContext() error = %v, want %v", err, want)
	}
}

func TestCommandPassesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := command.New("knowledge", func(runCtx context.Context) error {
		return runCtx.Err()
	})
	cmd.SetArgs(nil)
	if err := cmd.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

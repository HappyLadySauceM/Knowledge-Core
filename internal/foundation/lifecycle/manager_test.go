package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/lifecycle"
)

func TestManagerStopsInReverseOrder(t *testing.T) {
	var calls []string
	manager := &lifecycle.Manager{}
	for _, name := range []string{"database", "broker", "server"} {
		name := name
		if err := manager.Add(lifecycle.Hook{
			Name:  name,
			Start: func(context.Context) error { calls = append(calls, "start:"+name); return nil },
			Stop:  func(context.Context) error { calls = append(calls, "stop:"+name); return nil },
		}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := []string{"start:database", "start:broker", "start:server", "stop:server", "stop:broker", "stop:database"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestManagerRollsBackAfterStartFailure(t *testing.T) {
	var calls []string
	manager := &lifecycle.Manager{}
	_ = manager.Add(lifecycle.Hook{Name: "database", Start: func(context.Context) error { return nil }, Stop: func(context.Context) error {
		calls = append(calls, "stop:database")
		return nil
	}})
	_ = manager.Add(lifecycle.Hook{Name: "broker", Start: func(context.Context) error { return errors.New("unavailable") }})

	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("Start() succeeded")
	}
	if !reflect.DeepEqual(calls, []string{"stop:database"}) {
		t.Fatalf("rollback calls = %#v", calls)
	}
}

func TestManagerWithoutHooksStillTracksRunningState(t *testing.T) {
	manager := &lifecycle.Manager{}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.Start(context.Background()); err == nil {
		t.Fatal("second Start() succeeded")
	}
	if err := manager.Add(lifecycle.Hook{Name: "late"}); err == nil {
		t.Fatal("Add() after Start() succeeded")
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

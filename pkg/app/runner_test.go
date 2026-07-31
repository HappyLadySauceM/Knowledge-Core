package app

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseRollsBackComponentsAndResourcesInReverseOrder(t *testing.T) {
	runtime := newRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second)
	var mu sync.Mutex
	var order []string
	record := func(value string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, value)
	}

	for _, name := range []string{"first", "second"} {
		name := name
		if err := runtime.AddComponent(ComponentFuncs{
			ComponentName: name,
			ServeFunc:     func() error { return nil },
			ShutdownFunc: func(context.Context) error {
				record("component:" + name)
				return nil
			},
		}); err != nil {
			t.Fatalf("AddComponent() error = %v", err)
		}
		if err := runtime.AddCleanup(name, func(context.Context) error {
			record("resource:" + name)
			return nil
		}); err != nil {
			t.Fatalf("AddCleanup() error = %v", err)
		}
	}

	if err := runtime.close(context.Background()); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	want := []string{"component:second", "component:first", "resource:second", "resource:first"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("close order = %#v, want %#v", order, want)
	}
	if err := runtime.close(context.Background()); err != nil {
		t.Fatalf("second close() error = %v", err)
	}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("second close repeated hooks: %#v", order)
	}
}

func TestRunGivesResourcesAnIndependentShutdownDeadline(t *testing.T) {
	const timeout = 100 * time.Millisecond
	runtime := newRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, timeout)
	serveDone := make(chan struct{})
	if err := runtime.AddComponent(ComponentFuncs{
		ComponentName: "slow-component",
		ServeFunc: func() error {
			<-serveDone
			return nil
		},
		ShutdownFunc: func(ctx context.Context) error {
			timer := time.NewTimer(timeout / 2)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
			close(serveDone)
			return nil
		},
	}); err != nil {
		t.Fatalf("AddComponent() error = %v", err)
	}
	cleanupHadTime := false
	if err := runtime.AddCleanup("resource", func(ctx context.Context) error {
		cleanupHadTime = ctx.Err() == nil
		return nil
	}); err != nil {
		t.Fatalf("AddCleanup() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.run(ctx, context.Background()) }()
	waitUntilReady(t, runtime)
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !cleanupHadTime {
		t.Fatal("cleanup received the expired component shutdown context")
	}
}

func TestRunWaitsForComponentReadiness(t *testing.T) {
	runtime := newRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second)
	readyCalled := make(chan struct{})
	readyGate := make(chan struct{})
	serveDone := make(chan struct{})
	if err := runtime.AddComponent(ComponentFuncs{
		ComponentName: "gated",
		ServeFunc: func() error {
			<-serveDone
			return nil
		},
		ReadyFunc: func(ctx context.Context) error {
			close(readyCalled)
			select {
			case <-readyGate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		ShutdownFunc: func(context.Context) error {
			close(serveDone)
			return nil
		},
	}); err != nil {
		t.Fatalf("AddComponent() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.run(ctx, context.Background()) }()
	<-readyCalled
	if err := runtime.Health.Ready(context.Background()); err == nil {
		t.Fatal("health reported ready before the component readiness handshake")
	}
	close(readyGate)
	waitUntilReady(t, runtime)
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunRecoversServePanicAndRollsBackResources(t *testing.T) {
	runtime := newRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second)
	var cleaned atomic.Bool
	if err := runtime.AddComponent(ComponentFuncs{
		ComponentName: "panic",
		ServeFunc: func() error {
			panic("boom")
		},
	}); err != nil {
		t.Fatalf("AddComponent() error = %v", err)
	}
	if err := runtime.AddCleanup("resource", func(context.Context) error {
		cleaned.Store(true)
		return nil
	}); err != nil {
		t.Fatalf("AddCleanup() error = %v", err)
	}

	err := runtime.run(context.Background(), context.Background())
	if err == nil || !strings.Contains(err.Error(), `component "panic" serve panic: boom`) {
		t.Fatalf("run() error = %v, want recovered serve panic", err)
	}
	if !cleaned.Load() {
		t.Fatal("resource cleanup did not run after the component panic")
	}
}

func TestRunLeavesResourcesOpenWhenComponentDoesNotExit(t *testing.T) {
	const timeout = 30 * time.Millisecond
	runtime := newRuntime(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, timeout)
	serveDone := make(chan struct{})
	var cleaned atomic.Bool
	if err := runtime.AddComponent(ComponentFuncs{
		ComponentName: "stuck",
		ServeFunc: func() error {
			<-serveDone
			return nil
		},
		ShutdownFunc: func(context.Context) error { return nil },
	}); err != nil {
		t.Fatalf("AddComponent() error = %v", err)
	}
	if err := runtime.AddCleanup("resource", func(context.Context) error {
		cleaned.Store(true)
		return nil
	}); err != nil {
		t.Fatalf("AddCleanup() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- runtime.run(ctx, context.Background()) }()
	waitUntilReady(t, runtime)
	cancel()
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "resources left open") {
		t.Fatalf("run() error = %v, want unsafe-cleanup error", err)
	}
	if cleaned.Load() {
		t.Fatal("resource cleanup ran while a component was still serving")
	}
	close(serveDone)
}

func waitUntilReady(t *testing.T, runtime *Runtime) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if runtime.Health.Ready(context.Background()) == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("runtime did not become ready")
}

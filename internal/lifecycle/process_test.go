package lifecycle_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/lifecycle"
)

func TestProcessDrainsBeforeClosingResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan struct{})
	var calls []string
	process := lifecycle.Process{
		SetServing: func(serving bool) {
			calls = append(calls, "serving:"+map[bool]string{true: "true", false: "false"}[serving])
		},
		Serve: func() error {
			<-serveDone
			return nil
		},
		Shutdown: func(context.Context) error {
			calls = append(calls, "shutdown")
			close(serveDone)
			return nil
		},
		Close: func(context.Context) error {
			calls = append(calls, "close")
			return nil
		},
	}
	cancel()
	if err := lifecycle.Run(ctx, time.Second, process); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []string{"serving:true", "serving:false", "shutdown", "close"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestProcessReturnsServeAndCleanupErrors(t *testing.T) {
	serveErr := errors.New("serve failed")
	shutdownErr := errors.New("shutdown failed")
	closeErr := errors.New("close failed")
	err := lifecycle.Run(context.Background(), time.Second, lifecycle.Process{
		Serve:    func() error { return serveErr },
		Shutdown: func(context.Context) error { return shutdownErr },
		Close:    func(context.Context) error { return closeErr },
	})
	for _, want := range []error{serveErr, shutdownErr, closeErr} {
		if !errors.Is(err, want) {
			t.Fatalf("Run() error = %v, missing %v", err, want)
		}
	}
}

func TestProcessTimesOutWaitingForTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release := make(chan struct{})
	defer close(release)
	err := lifecycle.Run(ctx, 10*time.Millisecond, lifecycle.Process{
		Serve:    func() error { <-release; return nil },
		Shutdown: func(context.Context) error { return nil },
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestProcessValidatesConfiguration(t *testing.T) {
	//nolint:staticcheck // This negative test verifies the public nil-context guard.
	if err := lifecycle.Run(nil, time.Second, lifecycle.Process{Serve: func() error { return nil }}); err == nil {
		t.Fatal("Run() accepted a nil context")
	}
	if err := lifecycle.Run(context.Background(), 0, lifecycle.Process{Serve: func() error { return nil }}); err == nil {
		t.Fatal("Run() accepted a zero timeout")
	}
	if err := lifecycle.Run(context.Background(), time.Second, lifecycle.Process{}); err == nil {
		t.Fatal("Run() accepted a nil Serve")
	}
}

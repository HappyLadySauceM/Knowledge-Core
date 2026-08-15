package circuit_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
)

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	breaker := circuit.NewWithClock(func() time.Time { return now })

	for i := 0; i < circuit.DefaultFailureThreshold-1; i++ {
		if err := breaker.Allow(); err != nil {
			t.Fatalf("Allow() before threshold error = %v", err)
		}
		breaker.Failure()
		if breaker.State() != circuit.StateClosed {
			t.Fatalf("State() = %s, want closed", breaker.State())
		}
	}
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() at threshold error = %v", err)
	}
	breaker.Failure()
	if breaker.State() != circuit.StateOpen {
		t.Fatalf("State() = %s, want open", breaker.State())
	}
	if err := breaker.Allow(); !errors.Is(err, circuit.ErrOpen) {
		t.Fatalf("Allow() while open error = %v, want %v", err, circuit.ErrOpen)
	}
}

func TestBreakerSuccessResetsConsecutiveFailures(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	breaker := circuit.NewWithClock(func() time.Time { return now })
	for i := 0; i < circuit.DefaultFailureThreshold-1; i++ {
		if err := breaker.Allow(); err != nil {
			t.Fatalf("Allow() error = %v", err)
		}
		breaker.Failure()
	}
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	breaker.Success()
	for i := 0; i < circuit.DefaultFailureThreshold-1; i++ {
		if err := breaker.Allow(); err != nil {
			t.Fatalf("Allow() after reset error = %v", err)
		}
		breaker.Failure()
	}
	if breaker.State() != circuit.StateClosed {
		t.Fatalf("State() = %s, want closed after reset", breaker.State())
	}
}

func TestBreakerHalfOpenProbeClosesOnSuccess(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	breaker := circuit.NewWithClock(func() time.Time { return now })
	openBreaker(t, breaker)
	now = now.Add(circuit.DefaultOpenDuration)
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() after open duration error = %v", err)
	}
	if breaker.State() != circuit.StateHalfOpen {
		t.Fatalf("State() = %s, want half-open", breaker.State())
	}
	breaker.Success()
	if breaker.State() != circuit.StateClosed {
		t.Fatalf("State() = %s, want closed", breaker.State())
	}
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() after close error = %v", err)
	}
}

func TestBreakerHalfOpenProbeReopensOnFailure(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	breaker := circuit.NewWithClock(func() time.Time { return now })
	openBreaker(t, breaker)
	now = now.Add(circuit.DefaultOpenDuration)
	if err := breaker.Allow(); err != nil {
		t.Fatalf("Allow() after open duration error = %v", err)
	}
	breaker.Failure()
	if breaker.State() != circuit.StateOpen {
		t.Fatalf("State() = %s, want open", breaker.State())
	}
	if err := breaker.Allow(); !errors.Is(err, circuit.ErrOpen) {
		t.Fatalf("Allow() after failed probe error = %v, want %v", err, circuit.ErrOpen)
	}
}

func TestBreakerAllowsOnlyOneHalfOpenProbe(t *testing.T) {
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	breaker := circuit.NewWithClock(func() time.Time { return now })
	openBreaker(t, breaker)
	now = now.Add(circuit.DefaultOpenDuration)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := breaker.Allow(); err == nil {
				allowed.Add(1)
			} else if !errors.Is(err, circuit.ErrOpen) {
				t.Errorf("Allow() error = %v", err)
			}
		}()
	}
	wg.Wait()
	if got := allowed.Load(); got != 1 {
		t.Fatalf("half-open probes = %d, want 1", got)
	}
}

func openBreaker(t *testing.T, breaker *circuit.Breaker) {
	t.Helper()
	for i := 0; i < circuit.DefaultFailureThreshold; i++ {
		if err := breaker.Allow(); err != nil {
			t.Fatalf("Allow() error = %v", err)
		}
		breaker.Failure()
	}
	if breaker.State() != circuit.StateOpen {
		t.Fatalf("State() = %s, want open", breaker.State())
	}
}

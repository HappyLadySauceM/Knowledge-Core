package circuit_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestKitexMiddlewareOpensOnTransportFailuresAndSkipsDial(t *testing.T) {
	breaker := circuit.New()
	var calls atomic.Int32
	next := endpoint.Endpoint(func(context.Context, any, any) error {
		calls.Add(1)
		return errors.New("connection refused")
	})
	middleware := circuit.KitexClientMiddleware(breaker, nil)

	for i := 0; i < circuit.DefaultFailureThreshold; i++ {
		if err := middleware(next)(context.Background(), nil, nil); err == nil {
			t.Fatal("transport failure returned nil")
		}
	}
	if breaker.State() != circuit.StateOpen {
		t.Fatalf("State() = %s, want open", breaker.State())
	}
	if err := middleware(next)(context.Background(), nil, nil); !errors.Is(err, circuit.ErrOpen) {
		t.Fatalf("open circuit error = %v, want %v", err, circuit.ErrOpen)
	}
	if got := calls.Load(); got != int32(circuit.DefaultFailureThreshold) {
		t.Fatalf("next calls = %d, want %d", got, circuit.DefaultFailureThreshold)
	}
}

func TestKitexMiddlewareDoesNotTripOnBusinessErrors(t *testing.T) {
	breaker := circuit.New()
	next := endpoint.Endpoint(func(context.Context, any, any) error {
		return kerrors.NewBizStatusError(20004, "document not found")
	})
	middleware := circuit.KitexClientMiddleware(breaker, nil)
	for i := 0; i < circuit.DefaultFailureThreshold+2; i++ {
		if err := middleware(next)(context.Background(), nil, nil); err == nil {
			t.Fatal("business error returned nil")
		}
	}
	if breaker.State() != circuit.StateClosed {
		t.Fatalf("State() = %s, want closed", breaker.State())
	}
}

func TestKitexMiddlewareReportsState(t *testing.T) {
	breaker := circuit.New()
	var last circuit.State
	middleware := circuit.KitexClientMiddleware(breaker, func(state circuit.State) {
		last = state
	})
	next := endpoint.Endpoint(func(context.Context, any, any) error {
		return errors.New("dial tcp: connection refused")
	})
	for i := 0; i < circuit.DefaultFailureThreshold; i++ {
		_ = middleware(next)(context.Background(), nil, nil)
	}
	if last != circuit.StateOpen {
		t.Fatalf("observed state = %s, want open", last)
	}
}

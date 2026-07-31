package nats

import (
	"context"
	"errors"
	"testing"
	"time"
)

type contextValueKey struct{}

func TestSubscriptionLifetimeIgnoresSetupDeadlineAndKeepsValues(t *testing.T) {
	t.Parallel()
	setupValues := context.WithValue(context.Background(), contextValueKey{}, "request-value")
	setupCtx, cancelSetup := context.WithDeadline(setupValues, time.Now().Add(-time.Second))
	defer cancelSetup()
	if !errors.Is(setupCtx.Err(), context.DeadlineExceeded) {
		t.Fatalf("setup context error = %v, want deadline exceeded", setupCtx.Err())
	}

	broker := newBrokerLifetime()
	defer broker.stop()
	lifetimeCtx, cancelLifetime := newSubscriptionLifetime(broker.context(), setupCtx)
	defer cancelLifetime()
	if err := lifetimeCtx.Err(); err != nil {
		t.Fatalf("subscription lifetime inherited setup cancellation: %v", err)
	}
	if got := lifetimeCtx.Value(contextValueKey{}); got != "request-value" {
		t.Fatalf("subscription lifetime value = %v, want request-value", got)
	}
	if _, ok := lifetimeCtx.Deadline(); ok {
		t.Fatal("subscription lifetime inherited setup deadline")
	}
}

func TestSubscriptionLifetimeCanceledByBroker(t *testing.T) {
	t.Parallel()
	broker := newBrokerLifetime()
	lifetimeCtx, cancelLifetime := newSubscriptionLifetime(broker.context(), context.Background())
	defer cancelLifetime()
	broker.stop()
	<-lifetimeCtx.Done()
	if !errors.Is(lifetimeCtx.Err(), context.Canceled) {
		t.Fatalf("subscription lifetime error = %v, want context.Canceled", lifetimeCtx.Err())
	}
}

func TestSubscriptionStopCancelsHandlerLifetimeAfterDrain(t *testing.T) {
	t.Parallel()
	broker := newBrokerLifetime()
	defer broker.stop()
	lifetimeCtx, cancelLifetime := newSubscriptionLifetime(broker.context(), context.Background())
	native := newFakeDrainSubscription()
	subscription := newSubscription(native, time.Minute, cancelLifetime)
	result := make(chan error, 1)
	go func() { result <- subscription.Stop(context.Background()) }()

	<-native.drainStarted
	select {
	case <-lifetimeCtx.Done():
		t.Fatal("Stop() canceled handler lifetime before drain completed")
	default:
	}
	native.finish()
	select {
	case <-lifetimeCtx.Done():
	default:
		t.Fatal("closed handler did not cancel handler lifetime")
	}
	if err := <-result; err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func TestShutdownCancelsBrokerLifetimeAfterDrainCompletes(t *testing.T) {
	t.Parallel()
	lifetime := newBrokerLifetime()
	var state shutdown
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- shutdownWithLifetime(&state, &lifetime, func() error {
			close(started)
			<-release
			return nil
		})
	}()

	<-started
	select {
	case <-lifetime.context().Done():
		t.Fatal("broker lifetime canceled before drain completed")
	default:
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("shutdownWithLifetime() error = %v", err)
	}
	<-lifetime.context().Done()
	if !errors.Is(lifetime.context().Err(), context.Canceled) {
		t.Fatalf("broker lifetime error = %v, want context.Canceled", lifetime.context().Err())
	}
}

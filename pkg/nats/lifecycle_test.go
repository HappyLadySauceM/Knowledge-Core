package nats

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
)

func TestDrainConnectionWaitsForClosedHandler(t *testing.T) {
	t.Parallel()
	connection := newFakeDrainConnection()
	closed := make(chan struct{})
	deadline := make(chan time.Time)
	result := make(chan error, 1)
	go func() {
		result <- drainConnectionUntil(connection, closed, "test", time.Minute, deadline)
	}()

	<-connection.drainStarted
	select {
	case err := <-result:
		t.Fatalf("drainConnectionUntil() returned before CLOSED: %v", err)
	default:
	}
	connection.setStatus(natsclient.CLOSED)
	close(closed)
	if err := <-result; err != nil {
		t.Fatalf("drainConnectionUntil() error = %v", err)
	}
	if drainCalls, closeCalls := connection.calls(); drainCalls != 1 || closeCalls != 0 {
		t.Fatalf("calls = (drain=%d, close=%d), want (1, 0)", drainCalls, closeCalls)
	}
}

func TestDrainConnectionTimeoutForcesClosedState(t *testing.T) {
	t.Parallel()
	connection := newFakeDrainConnection()
	closed := make(chan struct{})
	deadline := make(chan time.Time, 1)
	result := make(chan error, 1)
	go func() {
		result <- drainConnectionUntil(connection, closed, "test", 42*time.Second, deadline)
	}()

	<-connection.drainStarted
	deadline <- time.Now()
	err := <-result
	if !errors.Is(err, natsclient.ErrDrainTimeout) {
		t.Fatalf("drainConnectionUntil() error = %v, want ErrDrainTimeout", err)
	}
	if status := connection.Status(); status != natsclient.CLOSED {
		t.Fatalf("connection status = %s, want CLOSED", status)
	}
	if drainCalls, closeCalls := connection.calls(); drainCalls != 1 || closeCalls != 1 {
		t.Fatalf("calls = (drain=%d, close=%d), want (1, 1)", drainCalls, closeCalls)
	}
}

func TestDrainConnectionAcceptsClosedStateWhenHandlerLags(t *testing.T) {
	t.Parallel()
	connection := newFakeDrainConnection()
	deadline := make(chan time.Time, 1)
	result := make(chan error, 1)
	go func() {
		result <- drainConnectionUntil(connection, make(chan struct{}), "test", time.Minute, deadline)
	}()

	<-connection.drainStarted
	connection.setStatus(natsclient.CLOSED)
	deadline <- time.Now()
	if err := <-result; err != nil {
		t.Fatalf("drainConnectionUntil() error = %v", err)
	}
	if _, closeCalls := connection.calls(); closeCalls != 0 {
		t.Fatalf("Close() calls = %d, want 0", closeCalls)
	}
}

func TestDrainConnectionStartFailureForcesClosedState(t *testing.T) {
	t.Parallel()
	startErr := errors.New("start drain")
	connection := newFakeDrainConnection()
	connection.drainErr = startErr
	err := drainConnectionUntil(connection, make(chan struct{}), "test", time.Minute, make(chan time.Time))
	if !errors.Is(err, startErr) {
		t.Fatalf("drainConnectionUntil() error = %v, want %v", err, startErr)
	}
	if status := connection.Status(); status != natsclient.CLOSED {
		t.Fatalf("connection status = %s, want CLOSED", status)
	}
}

func TestDrainConnectionReportsLibraryTimeout(t *testing.T) {
	t.Parallel()
	connection := newFakeDrainConnection()
	connection.lastErr = natsclient.ErrDrainTimeout
	closed := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- drainConnectionUntil(connection, closed, "test", time.Minute, make(chan time.Time))
	}()

	<-connection.drainStarted
	connection.setStatus(natsclient.CLOSED)
	close(closed)
	if err := <-result; !errors.Is(err, natsclient.ErrDrainTimeout) {
		t.Fatalf("drainConnectionUntil() error = %v, want ErrDrainTimeout", err)
	}
}

func TestShutdownConcurrentCallersShareCompletion(t *testing.T) {
	t.Parallel()
	var state shutdown
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	wantErr := errors.New("shutdown result")
	operation := func() error {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return wantErr
	}

	const workers = 16
	results := make(chan error, workers)
	for range workers {
		go func() { results <- state.wait(operation) }()
	}
	<-started
	close(release)
	for range workers {
		if err := <-results; !errors.Is(err, wantErr) {
			t.Fatalf("shutdown.wait() error = %v, want %v", err, wantErr)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("shutdown operation calls = %d, want 1", got)
	}
}

func TestOperationGateRejectsNewWorkAfterStop(t *testing.T) {
	t.Parallel()
	var gate operationGate
	if !gate.begin() {
		t.Fatal("operationGate.begin() = false before stop")
	}
	stopped := make(chan struct{})
	go func() {
		gate.stop()
		close(stopped)
	}()
	gate.end()
	<-stopped
	if gate.begin() {
		gate.end()
		t.Fatal("operationGate.begin() = true after stop")
	}
}

func TestSubscriptionStopWaitsForHandlerExitAndIsConcurrent(t *testing.T) {
	t.Parallel()
	native := newFakeDrainSubscription()
	subscription := newSubscription(native, time.Minute, nil)

	const workers = 16
	results := make(chan error, workers)
	for range workers {
		go func() { results <- subscription.Stop(context.Background()) }()
	}
	<-native.drainStarted
	select {
	case err := <-results:
		t.Fatalf("Stop() returned before subscription closed handler: %v", err)
	default:
	}
	native.finish()
	for range workers {
		if err := <-results; err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	}
	if got := native.calls(); got != 1 {
		t.Fatalf("Drain() calls = %d, want 1", got)
	}
	if got := native.unsubscribeCount(); got != 0 {
		t.Fatalf("Unsubscribe() calls = %d, want 0", got)
	}
}

func TestSubscriptionCanceledWaitDoesNotPoisonLaterStop(t *testing.T) {
	t.Parallel()
	native := newFakeDrainSubscription()
	subscription := newSubscription(native, time.Minute, nil)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() { firstResult <- subscription.Stop(firstCtx) }()

	<-native.drainStarted
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("first Stop() error = %v, want context.Canceled", err)
	}
	secondResult := make(chan error, 1)
	go func() { secondResult <- subscription.Stop(context.Background()) }()
	native.finish()
	if err := <-secondResult; err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if got := native.calls(); got != 1 {
		t.Fatalf("Drain() calls = %d, want 1", got)
	}
	if got := native.unsubscribeCount(); got != 1 {
		t.Fatalf("Unsubscribe() calls = %d, want 1", got)
	}
}

func TestSubscriptionStopHasConfiguredUpperBound(t *testing.T) {
	t.Parallel()
	native := newFakeDrainSubscription()
	subscription := newSubscription(native, time.Nanosecond, nil)
	err := subscription.Stop(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want context.DeadlineExceeded", err)
	}
	if got := native.unsubscribeCount(); got != 1 {
		t.Fatalf("Unsubscribe() calls = %d, want 1", got)
	}
}

func TestSubscriptionStopJoinsForceUnsubscribeFailure(t *testing.T) {
	t.Parallel()
	forceErr := errors.New("unsubscribe failed")
	native := newFakeDrainSubscription()
	native.unsubErr = forceErr
	subscription := newSubscription(native, time.Nanosecond, nil)
	err := subscription.Stop(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, forceErr) {
		t.Fatalf("Stop() error = %v, want deadline and force-unsubscribe errors", err)
	}
}

func TestSubscriptionAlreadyCanceledContextForcesUnsubscribe(t *testing.T) {
	t.Parallel()
	native := newFakeDrainSubscription()
	broker := newBrokerLifetime()
	defer broker.stop()
	lifetimeCtx, cancelLifetime := newSubscriptionLifetime(broker.context(), context.Background())
	subscription := newSubscription(native, time.Minute, cancelLifetime)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := subscription.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Stop() error = %v, want context.Canceled", err)
	}
	if got := native.calls(); got != 0 {
		t.Fatalf("Drain() calls = %d, want 0", got)
	}
	if got := native.unsubscribeCount(); got != 1 {
		t.Fatalf("Unsubscribe() calls = %d, want 1", got)
	}
	select {
	case <-lifetimeCtx.Done():
	default:
		t.Fatal("forced Stop() did not cancel handler lifetime")
	}
}

type fakeDrainConnection struct {
	mu           sync.Mutex
	status       natsclient.Status
	lastErr      error
	drainErr     error
	drainCalls   int
	closeCalls   int
	drainStarted chan struct{}
	startOnce    sync.Once
}

func newFakeDrainConnection() *fakeDrainConnection {
	return &fakeDrainConnection{
		status:       natsclient.CONNECTED,
		drainStarted: make(chan struct{}),
	}
}

func (c *fakeDrainConnection) Drain() error {
	c.mu.Lock()
	c.drainCalls++
	err := c.drainErr
	c.mu.Unlock()
	c.startOnce.Do(func() { close(c.drainStarted) })
	return err
}

func (c *fakeDrainConnection) Close() {
	c.mu.Lock()
	c.closeCalls++
	c.status = natsclient.CLOSED
	c.mu.Unlock()
}

func (c *fakeDrainConnection) Status() natsclient.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *fakeDrainConnection) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

func (c *fakeDrainConnection) setStatus(status natsclient.Status) {
	c.mu.Lock()
	c.status = status
	c.mu.Unlock()
}

func (c *fakeDrainConnection) calls() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drainCalls, c.closeCalls
}

type fakeDrainSubscription struct {
	mu           sync.Mutex
	valid        bool
	drainCalls   int
	unsubCalls   int
	unsubErr     error
	closed       func(string)
	drainStarted chan struct{}
	startOnce    sync.Once
	finishOnce   sync.Once
}

func newFakeDrainSubscription() *fakeDrainSubscription {
	return &fakeDrainSubscription{valid: true, drainStarted: make(chan struct{})}
}

func (s *fakeDrainSubscription) Drain() error {
	s.mu.Lock()
	s.drainCalls++
	s.mu.Unlock()
	s.startOnce.Do(func() { close(s.drainStarted) })
	return nil
}

func (s *fakeDrainSubscription) Unsubscribe() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unsubCalls++
	return s.unsubErr
}

func (s *fakeDrainSubscription) IsValid() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.valid
}

func (s *fakeDrainSubscription) SetClosedHandler(handler func(string)) {
	s.mu.Lock()
	s.closed = handler
	s.mu.Unlock()
}

func (s *fakeDrainSubscription) finish() {
	s.finishOnce.Do(func() {
		s.mu.Lock()
		s.valid = false
		handler := s.closed
		s.mu.Unlock()
		if handler != nil {
			handler("test")
		}
	})
}

func (s *fakeDrainSubscription) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainCalls
}

func (s *fakeDrainSubscription) unsubscribeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsubCalls
}

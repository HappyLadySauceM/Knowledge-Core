package etcd

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	kitexregistry "github.com/cloudwego/kitex/pkg/registry"
)

func TestNewLifecycleRegistryRejectsNilDelegate(t *testing.T) {
	t.Parallel()

	registry, err := newLifecycleRegistry(nil)
	if registry != nil || !errors.Is(err, ErrLifecycleRegistryUnavailable) {
		t.Fatalf("NewLifecycleRegistry(nil) = (%v, %v), want unavailable error", registry, err)
	}
}

func TestLifecycleRegistrySuccessOpensBarrierAndDeduplicates(t *testing.T) {
	t.Parallel()

	delegate := &fakeKitexRegistry{}
	registry := mustLifecycleRegistry(t, delegate)
	info := testRegistryInfo(9001)
	waitResult := startObservedWait(t, registry)

	if err := registry.Register(info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := awaitError(t, waitResult); err != nil {
		t.Fatalf("WaitRegistered() error = %v", err)
	}

	equivalent := testRegistryInfo(9001)
	if err := registry.Register(equivalent); err != nil {
		t.Fatalf("duplicate Register() error = %v", err)
	}
	if got := delegate.registerCount(); got != 1 {
		t.Fatalf("delegate Register() calls = %d, want 1", got)
	}

	if err := registry.Deregister(equivalent); err != nil {
		t.Fatalf("Deregister() error = %v", err)
	}
	if err := registry.Deregister(info); err != nil {
		t.Fatalf("repeated Deregister() error = %v", err)
	}
	if got := delegate.deregisterCount(); got != 1 {
		t.Fatalf("delegate Deregister() calls = %d, want 1", got)
	}
	if err := registry.WaitRegistered(context.Background()); err != nil {
		t.Fatalf("WaitRegistered() after shutdown error = %v", err)
	}
}

func TestLifecycleRegistryRegisterFailureReachesExistingWaiter(t *testing.T) {
	t.Parallel()

	registerErr := errors.New("register failed")
	delegate := &fakeKitexRegistry{
		registerFn: func(*kitexregistry.Info) error {
			return registerErr
		},
	}
	registry := mustLifecycleRegistry(t, delegate)
	waitResult := startObservedWait(t, registry)

	if err := registry.Register(testRegistryInfo(9002)); err != registerErr {
		t.Fatalf("Register() error = %v, want %v", err, registerErr)
	}
	if err := awaitError(t, waitResult); err != registerErr {
		t.Fatalf("WaitRegistered() error = %v, want original error %v", err, registerErr)
	}
	if err := registry.Deregister(testRegistryInfo(9002)); err != nil {
		t.Fatalf("Deregister() after failed Register error = %v", err)
	}
	if got := delegate.deregisterCount(); got != 0 {
		t.Fatalf("delegate Deregister() calls = %d, want 0", got)
	}
}

func TestLifecycleRegistryWaiterKeepsAttemptFailureAcrossRetry(t *testing.T) {
	t.Parallel()

	registerErr := errors.New("first attempt failed")
	delegate := &fakeKitexRegistry{}
	delegate.registerFn = func(*kitexregistry.Info) error {
		if delegate.registerCount() == 1 {
			return registerErr
		}
		return nil
	}
	registry := mustLifecycleRegistry(t, delegate)
	waitResult := startObservedWait(t, registry)

	if err := registry.Register(testRegistryInfo(9003)); err != registerErr {
		t.Fatalf("first Register() error = %v, want %v", err, registerErr)
	}
	if err := registry.Register(testRegistryInfo(9003)); err != nil {
		t.Fatalf("retry Register() error = %v", err)
	}
	if err := awaitError(t, waitResult); err != registerErr {
		t.Fatalf("existing WaitRegistered() error = %v, want %v", err, registerErr)
	}
	if err := registry.WaitRegistered(context.Background()); err != nil {
		t.Fatalf("new WaitRegistered() error = %v", err)
	}
}

func TestLifecycleRegistryEarlyDeregisterCancelsBarrier(t *testing.T) {
	t.Parallel()

	delegate := &fakeKitexRegistry{}
	registry := mustLifecycleRegistry(t, delegate)
	waitResult := startObservedWait(t, registry)
	info := testRegistryInfo(9004)

	if err := registry.Deregister(info); err != nil {
		t.Fatalf("early Deregister() error = %v", err)
	}
	if err := awaitError(t, waitResult); !errors.Is(err, ErrRegistrationCanceled) {
		t.Fatalf("WaitRegistered() error = %v, want canceled", err)
	}
	if err := registry.Register(info); !errors.Is(err, ErrRegistrationCanceled) {
		t.Fatalf("Register() after cancellation error = %v, want canceled", err)
	}
	if got := delegate.registerCount(); got != 0 {
		t.Fatalf("delegate Register() calls = %d, want 0", got)
	}
	if got := delegate.deregisterCount(); got != 0 {
		t.Fatalf("delegate Deregister() calls = %d, want 0", got)
	}
}

func TestLifecycleRegistryDoesNotDeregisterAnotherInstance(t *testing.T) {
	t.Parallel()

	first := testRegistryInfo(9005)
	second := testRegistryInfo(9006)
	firstErr := errors.New("first registration failed")
	delegate := &fakeKitexRegistry{
		registerFn: func(info *kitexregistry.Info) error {
			if info == first {
				return firstErr
			}
			return nil
		},
	}
	registry := mustLifecycleRegistry(t, delegate)

	if err := registry.Register(first); err != firstErr {
		t.Fatalf("first Register() error = %v, want %v", err, firstErr)
	}
	if err := registry.Register(second); err != nil {
		t.Fatalf("second Register() error = %v", err)
	}
	if err := registry.Deregister(first); !errors.Is(err, ErrRegistrationMismatch) {
		t.Fatalf("wrong-instance Deregister() error = %v, want mismatch", err)
	}
	if got := delegate.deregisterCount(); got != 0 {
		t.Fatalf("delegate Deregister() calls = %d, want 0", got)
	}
	if err := registry.Deregister(second); err != nil {
		t.Fatalf("second Deregister() error = %v", err)
	}
	if got := delegate.deregisterCount(); got != 1 {
		t.Fatalf("delegate Deregister() calls = %d, want 1", got)
	}
}

func TestLifecycleRegistryPreservesDeregisterErrorForRetry(t *testing.T) {
	t.Parallel()

	deregisterErr := errors.New("deregister failed")
	delegate := &fakeKitexRegistry{}
	delegate.deregisterFn = func(*kitexregistry.Info) error {
		if delegate.deregisterCount() == 1 {
			return deregisterErr
		}
		return nil
	}
	registry := mustLifecycleRegistry(t, delegate)
	info := testRegistryInfo(9007)
	other := testRegistryInfo(9008)

	if err := registry.Register(info); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Deregister(info); err != deregisterErr {
		t.Fatalf("first Deregister() error = %v, want original error %v", err, deregisterErr)
	}
	if err := registry.Register(other); !errors.Is(err, ErrRegistrationConflict) {
		t.Fatalf("Register() while deregistration is unresolved error = %v, want conflict", err)
	}
	if err := registry.Deregister(info); err != nil {
		t.Fatalf("retry Deregister() error = %v", err)
	}
	if got := delegate.deregisterCount(); got != 2 {
		t.Fatalf("delegate Deregister() calls = %d, want 2", got)
	}
}

func TestLifecycleRegistryConcurrentDuplicateOperations(t *testing.T) {
	t.Parallel()

	delegate := &fakeKitexRegistry{}
	registry := mustLifecycleRegistry(t, delegate)
	info := testRegistryInfo(9009)

	const workers = 32
	registerErrors := runConcurrently(workers, func() error {
		return registry.Register(info)
	})
	assertNoErrors(t, "Register", registerErrors)
	if got := delegate.registerCount(); got != 1 {
		t.Fatalf("concurrent delegate Register() calls = %d, want 1", got)
	}

	deregisterErrors := runConcurrently(workers, func() error {
		return registry.Deregister(info)
	})
	assertNoErrors(t, "Deregister", deregisterErrors)
	if got := delegate.deregisterCount(); got != 1 {
		t.Fatalf("concurrent delegate Deregister() calls = %d, want 1", got)
	}
}

func TestLifecycleRegistryConcurrentDifferentInstancesConflict(t *testing.T) {
	t.Parallel()

	delegate := &fakeKitexRegistry{}
	registry := mustLifecycleRegistry(t, delegate)
	instances := []*kitexregistry.Info{testRegistryInfo(9011), testRegistryInfo(9012)}
	start := make(chan struct{})
	results := make(chan error, len(instances))
	var workers sync.WaitGroup
	workers.Add(len(instances))
	for _, info := range instances {
		go func() {
			defer workers.Done()
			<-start
			results <- registry.Register(info)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRegistrationConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Register() unexpected error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent Register() results = %d success, %d conflict; want 1 and 1", successes, conflicts)
	}
	if got := delegate.registerCount(); got != 1 {
		t.Fatalf("delegate Register() calls = %d, want 1", got)
	}
}

func TestLifecycleRegistryWaitContextCancellation(t *testing.T) {
	t.Parallel()

	registry := mustLifecycleRegistry(t, &fakeKitexRegistry{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := registry.WaitRegistered(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitRegistered() error = %v, want context cancellation", err)
	}
	if err := registry.WaitRegistered(nil); err == nil { //nolint:staticcheck // Explicitly verifies the nil-context contract.
		t.Fatal("WaitRegistered(nil) error = nil")
	}
}

func TestNilLifecycleRegistryIsSafe(t *testing.T) {
	t.Parallel()

	var registry *LifecycleRegistry
	if err := registry.Register(testRegistryInfo(9010)); !errors.Is(err, ErrLifecycleRegistryUnavailable) {
		t.Fatalf("Register() error = %v, want unavailable", err)
	}
	if err := registry.Deregister(testRegistryInfo(9010)); !errors.Is(err, ErrLifecycleRegistryUnavailable) {
		t.Fatalf("Deregister() error = %v, want unavailable", err)
	}
	if err := registry.WaitRegistered(context.Background()); !errors.Is(err, ErrLifecycleRegistryUnavailable) {
		t.Fatalf("WaitRegistered() error = %v, want unavailable", err)
	}
}

type fakeKitexRegistry struct {
	mu              sync.Mutex
	registerFn      func(*kitexregistry.Info) error
	deregisterFn    func(*kitexregistry.Info) error
	registerCalls   []*kitexregistry.Info
	deregisterCalls []*kitexregistry.Info
}

func (r *fakeKitexRegistry) Register(info *kitexregistry.Info) error {
	r.mu.Lock()
	r.registerCalls = append(r.registerCalls, info)
	registerFn := r.registerFn
	r.mu.Unlock()
	if registerFn == nil {
		return nil
	}
	return registerFn(info)
}

func (r *fakeKitexRegistry) Deregister(info *kitexregistry.Info) error {
	r.mu.Lock()
	r.deregisterCalls = append(r.deregisterCalls, info)
	deregisterFn := r.deregisterFn
	r.mu.Unlock()
	if deregisterFn == nil {
		return nil
	}
	return deregisterFn(info)
}

func (r *fakeKitexRegistry) registerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.registerCalls)
}

func (r *fakeKitexRegistry) deregisterCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deregisterCalls)
}

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() {
		close(c.observed)
	})
	return c.Context.Done()
}

func mustLifecycleRegistry(t *testing.T, delegate kitexregistry.Registry) *LifecycleRegistry {
	t.Helper()
	registry, err := newLifecycleRegistry(delegate)
	if err != nil {
		t.Fatalf("NewLifecycleRegistry() error = %v", err)
	}
	return registry
}

func testRegistryInfo(port int) *kitexregistry.Info {
	return &kitexregistry.Info{
		ServiceName: "identity",
		Addr: &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: port,
		},
	}
}

func startObservedWait(t *testing.T, registry *LifecycleRegistry) <-chan error {
	t.Helper()
	baseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	ctx := &observedDoneContext{
		Context:  baseCtx,
		observed: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() {
		result <- registry.WaitRegistered(ctx)
	}()
	select {
	case <-ctx.observed:
		return result
	case <-baseCtx.Done():
		t.Fatalf("WaitRegistered() did not begin: %v", baseCtx.Err())
		return nil
	}
}

func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for operation")
		return nil
	}
}

func runConcurrently(workers int, operation func() error) []error {
	start := make(chan struct{})
	results := make(chan error, workers)
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for range workers {
		go func() {
			defer workersDone.Done()
			<-start
			results <- operation()
		}()
	}
	close(start)
	workersDone.Wait()
	close(results)

	errors := make([]error, 0, workers)
	for err := range results {
		errors = append(errors, err)
	}
	return errors
}

func assertNoErrors(t *testing.T, operation string, errs []error) {
	t.Helper()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent %s() result %d error = %v", operation, index, err)
		}
	}
}

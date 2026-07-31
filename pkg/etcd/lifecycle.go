package etcd

import (
	"context"
	"errors"
	"fmt"
	"sync"

	kitexregistry "github.com/cloudwego/kitex/pkg/registry"
)

var (
	// ErrRegistrationCanceled reports that the registry lifecycle ended before
	// any registration completed successfully.
	ErrRegistrationCanceled = errors.New("registry registration canceled before success")
	// ErrRegistrationConflict reports an attempt to register a different
	// service instance while another instance is active.
	ErrRegistrationConflict = errors.New("another service instance is already registered")
	// ErrRegistrationMismatch reports an attempt to deregister an instance that
	// is different from the active registration.
	ErrRegistrationMismatch = errors.New("service instance does not match the active registration")
	// ErrLifecycleRegistryUnavailable reports a nil or uninitialized lifecycle
	// registry.
	ErrLifecycleRegistryUnavailable = errors.New("lifecycle registry is unavailable")
)

type lifecycleState uint8

const (
	lifecyclePending lifecycleState = iota
	lifecycleRegistering
	lifecycleRegistered
	lifecycleRegisterFailed
	lifecycleDeregistering
	lifecycleDeregistered
	lifecycleCanceled
)

// LifecycleRegistry serializes Kitex registry operations and exposes a
// registration barrier. It protects registry implementations that assume
// Deregister is called only after a successful Register.
//
// A LifecycleRegistry is constructed by Open and must
// not be copied after first use.
type LifecycleRegistry struct {
	delegate kitexregistry.Registry

	operationMu sync.Mutex
	stateMu     sync.Mutex
	state       lifecycleState
	attempt     *registrationAttempt

	active         bool
	activeInstance registrationIdentity
	everRegistered bool
}

var _ kitexregistry.Registry = (*LifecycleRegistry)(nil)

type registrationAttempt struct {
	done      chan struct{}
	err       error
	completed bool
}

type registrationIdentity struct {
	info        *kitexregistry.Info
	serviceName string
	network     string
	address     string
	valid       bool
}

// newLifecycleRegistry wraps delegate with concurrency-safe registration
// lifecycle handling.
func newLifecycleRegistry(delegate kitexregistry.Registry) (*LifecycleRegistry, error) {
	if delegate == nil {
		return nil, fmt.Errorf("create lifecycle registry: %w", ErrLifecycleRegistryUnavailable)
	}
	return &LifecycleRegistry{
		delegate: delegate,
		state:    lifecyclePending,
		attempt:  newRegistrationAttempt(),
	}, nil
}

// Register registers info once. Concurrent or repeated registration of the
// same instance is idempotent. A different instance is rejected while the
// current instance remains active.
func (r *LifecycleRegistry) Register(info *kitexregistry.Info) error {
	if err := r.available(); err != nil {
		return err
	}

	r.operationMu.Lock()
	defer r.operationMu.Unlock()

	instance := identifyRegistration(info)
	r.stateMu.Lock()
	if r.state == lifecycleCanceled && !r.everRegistered {
		r.stateMu.Unlock()
		return ErrRegistrationCanceled
	}
	if r.active {
		if sameRegistration(r.activeInstance, instance) {
			r.stateMu.Unlock()
			return nil
		}
		r.stateMu.Unlock()
		return fmt.Errorf("register service instance: %w", ErrRegistrationConflict)
	}
	if r.attempt.completed {
		r.attempt = newRegistrationAttempt()
	}
	attempt := r.attempt
	r.state = lifecycleRegistering
	r.stateMu.Unlock()

	err := r.delegate.Register(info)

	r.stateMu.Lock()
	if err != nil {
		r.state = lifecycleRegisterFailed
	} else {
		r.state = lifecycleRegistered
		r.active = true
		r.activeInstance = instance
		r.everRegistered = true
	}
	r.completeAttemptLocked(attempt, err)
	r.stateMu.Unlock()
	return err
}

// Deregister deregisters only the instance whose Register call succeeded. A
// delegate error is returned unchanged and keeps the registration active so a
// later call can retry it.
func (r *LifecycleRegistry) Deregister(info *kitexregistry.Info) error {
	if err := r.available(); err != nil {
		return err
	}

	r.operationMu.Lock()
	defer r.operationMu.Unlock()

	instance := identifyRegistration(info)
	r.stateMu.Lock()
	if !r.active {
		// Kitex can stop before its delayed Register call. Mark that startup as
		// canceled, but preserve a real Register error if one already occurred.
		if !r.everRegistered && r.state != lifecycleRegisterFailed && r.state != lifecycleCanceled {
			r.state = lifecycleCanceled
			r.completeAttemptLocked(r.attempt, ErrRegistrationCanceled)
		}
		r.stateMu.Unlock()
		return nil
	}
	if !sameRegistration(r.activeInstance, instance) {
		r.stateMu.Unlock()
		return fmt.Errorf("deregister service instance: %w", ErrRegistrationMismatch)
	}
	r.state = lifecycleDeregistering
	r.stateMu.Unlock()

	err := r.delegate.Deregister(info)

	r.stateMu.Lock()
	if err != nil {
		r.state = lifecycleRegistered
	} else {
		r.state = lifecycleDeregistered
		r.active = false
		r.activeInstance = registrationIdentity{}
	}
	r.stateMu.Unlock()
	return err
}

// WaitRegistered waits until a delegate Register call succeeds or the current
// startup attempt fails. Once registration has succeeded, the barrier remains
// open for the lifetime of this wrapper, including during graceful shutdown.
func (r *LifecycleRegistry) WaitRegistered(ctx context.Context) error {
	if err := r.available(); err != nil {
		return err
	}
	if ctx == nil {
		return errors.New("wait for registry registration: context is required")
	}

	r.stateMu.Lock()
	if r.everRegistered {
		r.stateMu.Unlock()
		return nil
	}
	attempt := r.attempt
	r.stateMu.Unlock()

	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
		// Prefer a completed registration outcome that raced with context
		// cancellation, and then prefer a successful earlier registration.
		select {
		case <-attempt.done:
			return attempt.err
		default:
		}
		r.stateMu.Lock()
		registered := r.everRegistered
		r.stateMu.Unlock()
		if registered {
			return nil
		}
		return fmt.Errorf("wait for registry registration: %w", ctx.Err())
	}
}

func (r *LifecycleRegistry) available() error {
	if r == nil || r.delegate == nil {
		return ErrLifecycleRegistryUnavailable
	}
	return nil
}

func newRegistrationAttempt() *registrationAttempt {
	return &registrationAttempt{done: make(chan struct{})}
}

func (r *LifecycleRegistry) completeAttemptLocked(attempt *registrationAttempt, err error) {
	if attempt.completed {
		return
	}
	attempt.err = err
	attempt.completed = true
	close(attempt.done)
}

func identifyRegistration(info *kitexregistry.Info) registrationIdentity {
	identity := registrationIdentity{info: info}
	if info == nil || info.Addr == nil {
		return identity
	}
	identity.serviceName = info.ServiceName
	identity.network = info.Addr.Network()
	identity.address = info.Addr.String()
	identity.valid = true
	return identity
}

func sameRegistration(left, right registrationIdentity) bool {
	if left.valid && right.valid {
		return left.serviceName == right.serviceName &&
			left.network == right.network &&
			left.address == right.address
	}
	return left.info == right.info
}

package nats

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	natsclient "github.com/nats-io/nats.go"
)

type drainableSubscription interface {
	Drain() error
	Unsubscribe() error
	IsValid() bool
	SetClosedHandler(func(string))
}

// Subscription owns a concrete NATS subscription and drains it on Stop. A
// successful Stop guarantees that the nats.go delivery loop and its current
// callback have exited.
type Subscription struct {
	subscription drainableSubscription
	startOnce    sync.Once
	startErr     error
	forceOnce    sync.Once
	forceErr     error
	cancelOnce   sync.Once
	cancel       context.CancelFunc
	done         <-chan struct{}
	timeout      time.Duration
}

func newSubscription(
	subscription drainableSubscription,
	timeout time.Duration,
	cancel context.CancelFunc,
) *Subscription {
	done := make(chan struct{})
	var doneOnce sync.Once
	result := &Subscription{subscription: subscription, cancel: cancel, done: done, timeout: timeout}
	complete := func() {
		result.cancelLifetime()
		doneOnce.Do(func() { close(done) })
	}
	if subscription == nil {
		complete()
		return result
	}

	// nats.go invokes this handler only after an asynchronous subscription's
	// delivery loop has exited. Install it before exposing the subscription.
	subscription.SetClosedHandler(func(string) { complete() })
	if !subscription.IsValid() {
		complete()
	}
	return result
}

func (s *Subscription) Stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("stop nats subscription: context is required")
	}
	if s == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
	select {
	case <-s.done:
		return nil
	default:
	}
	if err := ctx.Err(); err != nil {
		return s.forceStop(err)
	}
	if s.subscription == nil {
		return nil
	}

	s.startOnce.Do(func() {
		err := s.subscription.Drain()
		if err != nil && !s.subscription.IsValid() &&
			(errors.Is(err, natsclient.ErrBadSubscription) || errors.Is(err, natsclient.ErrConnectionClosed)) {
			return
		}
		s.startErr = err
	})
	if s.startErr != nil {
		return fmt.Errorf("drain nats subscription: %w", s.startErr)
	}

	waitCtx, cancel := boundedContext(ctx, s.timeout)
	defer cancel()
	select {
	case <-s.done:
		return nil
	case <-waitCtx.Done():
		return s.forceStop(waitCtx.Err())
	}
}

func (s *Subscription) forceStop(waitErr error) error {
	s.cancelLifetime()
	s.forceOnce.Do(func() {
		if s.subscription == nil {
			return
		}
		err := s.subscription.Unsubscribe()
		if err != nil && !s.subscription.IsValid() &&
			(errors.Is(err, natsclient.ErrBadSubscription) || errors.Is(err, natsclient.ErrConnectionClosed)) {
			return
		}
		s.forceErr = err
	})
	result := fmt.Errorf("stop nats subscription before drain completed: %w", waitErr)
	if s.forceErr != nil {
		result = errors.Join(result, fmt.Errorf("force unsubscribe nats subscription: %w", s.forceErr))
	}
	return result
}

func (s *Subscription) cancelLifetime() {
	if s == nil {
		return
	}
	s.cancelOnce.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

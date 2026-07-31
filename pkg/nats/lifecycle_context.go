package nats

import (
	"context"
	"time"
)

type brokerLifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func newBrokerLifetime() brokerLifetime {
	ctx, cancel := context.WithCancel(context.Background())
	return brokerLifetime{ctx: ctx, cancel: cancel}
}

func (l *brokerLifetime) context() context.Context {
	if l == nil || l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

func (l *brokerLifetime) stop() {
	if l != nil && l.cancel != nil {
		l.cancel()
	}
}

func shutdownWithLifetime(
	state *shutdown,
	lifetime *brokerLifetime,
	operation func() error,
) error {
	return state.wait(func() error {
		defer lifetime.stop()
		return operation()
	})
}

// valueOverlayContext takes cancellation and deadlines from lifecycle while
// preserving request-scoped values from values.
type valueOverlayContext struct {
	lifecycle context.Context
	values    context.Context
}

func (c valueOverlayContext) Deadline() (time.Time, bool) { return c.lifecycle.Deadline() }
func (c valueOverlayContext) Done() <-chan struct{}       { return c.lifecycle.Done() }
func (c valueOverlayContext) Err() error                  { return c.lifecycle.Err() }
func (c valueOverlayContext) Value(key any) any           { return c.values.Value(key) }

func newSubscriptionLifetime(
	brokerCtx context.Context,
	setupCtx context.Context,
) (context.Context, context.CancelFunc) {
	if brokerCtx == nil {
		brokerCtx = context.Background()
	}
	if setupCtx == nil {
		setupCtx = context.Background()
	}
	base := valueOverlayContext{
		lifecycle: brokerCtx,
		values:    context.WithoutCancel(setupCtx),
	}
	return context.WithCancel(base)
}

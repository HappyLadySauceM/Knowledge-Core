package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	natsclient "github.com/nats-io/nats.go"
)

type RealtimeBus struct {
	conn          *natsclient.Conn
	closed        <-chan struct{}
	logger        *slog.Logger
	timeout       time.Duration
	drainTimeout  time.Duration
	operations    operationGate
	shutdownState shutdown
	lifetime      brokerLifetime
}

func OpenRealtime(ctx context.Context, opts option.NATSOptions, logger *slog.Logger) (*RealtimeBus, error) {
	connection, err := connect(ctx, opts, logger)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RealtimeBus{
		conn:         connection.client,
		closed:       connection.closed,
		logger:       logger,
		timeout:      opts.RequestTimeout,
		drainTimeout: opts.DrainTimeout,
		lifetime:     newBrokerLifetime(),
	}, nil
}

func (b *RealtimeBus) Publish(ctx context.Context, subject string, payload []byte) error {
	if b == nil || b.conn == nil {
		return errors.New("publish realtime nats message: bus is closed")
	}
	if !b.operations.begin() {
		return errors.New("publish realtime nats message: bus is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return errors.New("publish realtime nats message: context is required")
	}
	if subject == "" {
		return errors.New("publish realtime nats message: subject is required")
	}
	msg := natsclient.NewMsg(subject)
	msg.Data = append([]byte(nil), payload...)
	injectTrace(ctx, msg.Header)
	if err := b.conn.PublishMsg(msg); err != nil {
		return fmt.Errorf("publish realtime nats message: %w", err)
	}
	flushCtx, cancel := boundedContext(ctx, b.timeout)
	defer cancel()
	if err := b.conn.FlushWithContext(flushCtx); err != nil {
		return fmt.Errorf("flush realtime nats message: %w", err)
	}
	return nil
}

func (b *RealtimeBus) Subscribe(ctx context.Context, subject string, handler RealtimeHandler) (*Subscription, error) {
	if b == nil || b.conn == nil {
		return nil, errors.New("subscribe realtime nats: bus is closed")
	}
	if !b.operations.begin() {
		return nil, errors.New("subscribe realtime nats: bus is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return nil, errors.New("subscribe realtime nats: context is required")
	}
	if subject == "" || handler == nil {
		return nil, errors.New("subscribe realtime nats: subject and handler are required")
	}
	handlerCtx, cancelHandler := newSubscriptionLifetime(b.lifetime.context(), ctx)
	sub, err := b.conn.Subscribe(subject, func(msg *natsclient.Msg) {
		messageCtx := extractTrace(handlerCtx, msg.Header)
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					b.logger.ErrorContext(messageCtx, "realtime nats handler panicked", slog.String("subject", msg.Subject), slog.Any("panic", recovered))
				}
			}()
			handler(messageCtx, msg.Subject, append([]byte(nil), msg.Data...))
		}()
	})
	if err != nil {
		cancelHandler()
		return nil, fmt.Errorf("subscribe realtime nats: %w", err)
	}
	flushCtx, cancel := boundedContext(ctx, b.timeout)
	defer cancel()
	if err := b.conn.FlushWithContext(flushCtx); err != nil {
		cancelHandler()
		_ = sub.Unsubscribe()
		return nil, fmt.Errorf("activate realtime nats subscription: %w", err)
	}
	return newSubscription(sub, b.drainTimeout, cancelHandler), nil
}

func (b *RealtimeBus) Ping(ctx context.Context) error {
	if b == nil || b.conn == nil {
		return errors.New("ping realtime nats connection: bus is closed")
	}
	if !b.operations.begin() {
		return errors.New("ping realtime nats connection: bus is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return errors.New("ping realtime nats connection: context is required")
	}
	pingCtx, cancel := boundedContext(ctx, b.timeout)
	defer cancel()
	if err := b.conn.FlushWithContext(pingCtx); err != nil {
		return fmt.Errorf("ping realtime nats connection: %w", err)
	}
	return nil
}

func (b *RealtimeBus) Close() error {
	if b == nil {
		return nil
	}
	b.operations.stop()
	return shutdownWithLifetime(&b.shutdownState, &b.lifetime, func() error {
		return drainConnection(b.conn, b.closed, "realtime", b.drainTimeout)
	})
}

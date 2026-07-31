// Package nats provides concrete JetStream and Core NATS messaging resources.
package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	natsclient "github.com/nats-io/nats.go"
)

type clientConnection struct {
	client *natsclient.Conn
	closed <-chan struct{}
}

type drainableConnection interface {
	Drain() error
	Close()
	Status() natsclient.Status
	LastError() error
}

// operationGate lets shutdown wait for setup/request operations that already
// entered, then rejects every operation that arrives after closing begins.
type operationGate struct {
	mu      sync.RWMutex
	closing bool
}

func (g *operationGate) begin() bool {
	g.mu.RLock()
	if g.closing {
		g.mu.RUnlock()
		return false
	}
	return true
}

func (g *operationGate) end() {
	g.mu.RUnlock()
}

func (g *operationGate) stop() {
	g.mu.Lock()
	g.closing = true
	g.mu.Unlock()
}

// shutdown starts one bounded shutdown operation and lets every concurrent
// caller wait for the same result.
type shutdown struct {
	once sync.Once
	done chan struct{}
	err  error
}

func (s *shutdown) wait(operation func() error) error {
	s.once.Do(func() {
		s.done = make(chan struct{})
		go func() {
			s.err = operation()
			close(s.done)
		}()
	})
	<-s.done
	return s.err
}

func connect(ctx context.Context, opts option.NATSOptions, logger *slog.Logger) (*clientConnection, error) {
	if ctx == nil {
		return nil, errors.New("connect nats: context is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("connect nats: invalid options: %w", err)
	}
	tlsConfig, err := opts.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("connect nats: invalid TLS configuration: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	closed := make(chan struct{})
	var closedOnce sync.Once
	connectOptions := []natsclient.Option{
		natsclient.Name(opts.Name),
		natsclient.Timeout(opts.ConnectTimeout),
		natsclient.MaxReconnects(opts.MaxReconnects),
		natsclient.ReconnectWait(opts.ReconnectWait),
		natsclient.PingInterval(opts.PingInterval),
		natsclient.MaxPingsOutstanding(opts.MaxPingsOut),
		natsclient.DrainTimeout(opts.DrainTimeout),
		natsclient.DisconnectErrHandler(func(_ *natsclient.Conn, err error) {
			logger.Warn("nats disconnected", slog.Any("error", err))
		}),
		natsclient.ReconnectHandler(func(conn *natsclient.Conn) {
			logger.Info("nats reconnected", slog.String("server", conn.ConnectedUrl()))
		}),
		natsclient.ClosedHandler(func(conn *natsclient.Conn) {
			closedOnce.Do(func() { close(closed) })
			if closeErr := conn.LastError(); closeErr != nil {
				logger.Warn("nats connection closed", slog.Any("error", closeErr))
			}
		}),
		natsclient.ErrorHandler(func(_ *natsclient.Conn, sub *natsclient.Subscription, asyncErr error) {
			attrs := []any{slog.Any("error", asyncErr)}
			if sub != nil {
				attrs = append(attrs, slog.String("subject", sub.Subject))
			}
			logger.Error("nats asynchronous error", attrs...)
		}),
	}
	if tlsConfig != nil {
		connectOptions = append(connectOptions, natsclient.Secure(tlsConfig))
	}
	switch {
	case opts.Token != "":
		connectOptions = append(connectOptions, natsclient.Token(opts.Token))
	case opts.CredentialsFile != "":
		connectOptions = append(connectOptions, natsclient.UserCredentials(opts.CredentialsFile))
	case opts.Username != "":
		connectOptions = append(connectOptions, natsclient.UserInfo(opts.Username, opts.Password))
	}

	conn, err := natsclient.Connect(strings.Join(opts.Servers, ","), connectOptions...)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	verifyCtx, cancel := context.WithTimeout(ctx, opts.ConnectTimeout)
	defer cancel()
	if err := conn.FlushWithContext(verifyCtx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("verify nats connection: %w", err)
	}
	return &clientConnection{client: conn, closed: closed}, nil
}

func boundedContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

func drainConnection(conn drainableConnection, closed <-chan struct{}, resource string, timeout time.Duration) error {
	if conn == nil || conn.Status() == natsclient.CLOSED {
		return nil
	}
	if timeout <= 0 {
		forceErr := forceClose(conn, resource)
		return errors.Join(
			fmt.Errorf("drain %s nats connection: timeout must be positive", resource),
			forceErr,
		)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	return drainConnectionUntil(conn, closed, resource, timeout, timer.C)
}

func drainConnectionUntil(
	conn drainableConnection,
	closed <-chan struct{},
	resource string,
	timeout time.Duration,
	deadline <-chan time.Time,
) error {
	if conn == nil || conn.Status() == natsclient.CLOSED {
		return nil
	}
	if err := conn.Drain(); err != nil {
		if errors.Is(err, natsclient.ErrConnectionClosed) && conn.Status() == natsclient.CLOSED {
			return nil
		}
		return errors.Join(
			fmt.Errorf("start draining %s nats connection: %w", resource, err),
			forceClose(conn, resource),
		)
	}

	select {
	case <-closed:
		if conn.Status() != natsclient.CLOSED {
			return errors.Join(
				fmt.Errorf("drain %s nats connection: closed handler fired before CLOSED state", resource),
				forceClose(conn, resource),
			)
		}
		if errors.Is(conn.LastError(), natsclient.ErrDrainTimeout) {
			return fmt.Errorf("drain %s nats connection: %w", resource, natsclient.ErrDrainTimeout)
		}
		return nil
	case <-deadline:
		// CLOSED is the authoritative state. The closed callback is dispatched
		// asynchronously and may lag behind the state transition.
		if conn.Status() == natsclient.CLOSED {
			if errors.Is(conn.LastError(), natsclient.ErrDrainTimeout) {
				return fmt.Errorf("drain %s nats connection: %w", resource, natsclient.ErrDrainTimeout)
			}
			return nil
		}
		timeoutErr := fmt.Errorf(
			"drain %s nats connection timed out after %s: %w",
			resource,
			timeout,
			natsclient.ErrDrainTimeout,
		)
		return errors.Join(timeoutErr, forceClose(conn, resource))
	}
}

func forceClose(conn drainableConnection, resource string) error {
	if conn == nil {
		return nil
	}
	conn.Close()
	if status := conn.Status(); status != natsclient.CLOSED {
		return fmt.Errorf("force close %s nats connection: status is %s, want CLOSED", resource, status)
	}
	return nil
}

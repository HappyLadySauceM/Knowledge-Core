package nats

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/messaging"
	natsclient "github.com/nats-io/nats.go"
)

type RealtimeBus struct {
	conn *natsclient.Conn
}

func OpenRealtime(cfg Config) (*RealtimeBus, error) {
	conn, err := connect(cfg)
	if err != nil {
		return nil, err
	}
	return &RealtimeBus{conn: conn}, nil
}

func (b *RealtimeBus) Publish(ctx context.Context, subject string, payload []byte) error {
	if subject == "" {
		return errors.New("publish realtime nats message: subject is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.conn.Publish(subject, payload); err != nil {
		return fmt.Errorf("publish realtime nats message: %w", err)
	}
	if err := b.conn.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("flush realtime nats message: %w", err)
	}
	return nil
}

func (b *RealtimeBus) Subscribe(ctx context.Context, subject string, handler messaging.RealtimeHandler) (messaging.Subscription, error) {
	if subject == "" || handler == nil {
		return nil, errors.New("subscribe realtime nats: subject and handler are required")
	}
	sub, err := b.conn.Subscribe(subject, func(msg *natsclient.Msg) {
		handler(ctx, msg.Subject, append([]byte(nil), msg.Data...))
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe realtime nats: %w", err)
	}
	return &subscription{subscription: sub, conn: b.conn}, nil
}

func (b *RealtimeBus) Close() error {
	if err := b.conn.Drain(); err != nil {
		b.conn.Close()
		return fmt.Errorf("drain realtime nats connection: %w", err)
	}
	return nil
}

func (b *RealtimeBus) Ping(ctx context.Context) error {
	if err := b.conn.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("ping realtime nats connection: %w", err)
	}
	return nil
}

var _ messaging.RealtimeBus = (*RealtimeBus)(nil)

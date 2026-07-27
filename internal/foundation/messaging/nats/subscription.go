package nats

import (
	"context"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/messaging"
	natsclient "github.com/nats-io/nats.go"
)

type subscription struct {
	subscription *natsclient.Subscription
	conn         *natsclient.Conn
}

func (s *subscription) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.subscription.Drain(); err != nil {
		return fmt.Errorf("drain nats subscription: %w", err)
	}
	if err := s.conn.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("flush nats subscription: %w", err)
	}
	return nil
}

var _ messaging.Subscription = (*subscription)(nil)

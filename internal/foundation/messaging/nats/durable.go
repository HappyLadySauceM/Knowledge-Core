package nats

import (
	"context"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/messaging"
	natsclient "github.com/nats-io/nats.go"
)

type DurableBroker struct {
	conn *natsclient.Conn
	js   natsclient.JetStreamContext
}

func OpenDurable(cfg Config) (*DurableBroker, error) {
	conn, err := connect(cfg)
	if err != nil {
		return nil, err
	}
	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open nats JetStream: %w", err)
	}
	return &DurableBroker{conn: conn, js: js}, nil
}

func (b *DurableBroker) Publish(ctx context.Context, message messaging.Message, opts messaging.PublishOptions) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	msg := natsclient.NewMsg(message.Subject)
	msg.Data = append([]byte(nil), message.Body...)
	for key, value := range message.Headers {
		msg.Header.Set(key, value)
	}
	if message.ContentType != "" {
		msg.Header.Set("Content-Type", message.ContentType)
	}
	deduplicationID := opts.DeduplicationID
	if deduplicationID == "" {
		deduplicationID = message.ID
	}
	if deduplicationID != "" {
		msg.Header.Set(natsclient.MsgIdHdr, deduplicationID)
	}
	if message.ID != "" {
		msg.Header.Set("X-Message-ID", message.ID)
	}
	if _, err := b.js.PublishMsg(msg, natsclient.Context(ctx)); err != nil {
		return fmt.Errorf("publish durable nats message: %w", err)
	}
	return nil
}

func (b *DurableBroker) Subscribe(ctx context.Context, cfg messaging.ConsumerConfig, handler messaging.Handler) (messaging.Subscription, error) {
	if err := validateConsumer(cfg, handler); err != nil {
		return nil, err
	}
	opts := []natsclient.SubOpt{
		natsclient.ManualAck(),
		natsclient.AckExplicit(),
		natsclient.Durable(cfg.Durable),
		natsclient.BindStream(cfg.Stream),
	}
	if cfg.AckWait > 0 {
		opts = append(opts, natsclient.AckWait(cfg.AckWait))
	}
	if cfg.MaxDeliver > 0 {
		opts = append(opts, natsclient.MaxDeliver(cfg.MaxDeliver))
	}

	callback := func(msg *natsclient.Msg) {
		delivery := newDelivery(b.js, msg, cfg.DeadLetterSubject)
		handler(ctx, delivery)
		if !delivery.isSettled() {
			_ = delivery.Nack(ctx, 0)
		}
	}

	var (
		sub *natsclient.Subscription
		err error
	)
	if cfg.Queue == "" {
		sub, err = b.js.Subscribe(cfg.Subject, callback, opts...)
	} else {
		sub, err = b.js.QueueSubscribe(cfg.Subject, cfg.Queue, callback, opts...)
	}
	if err != nil {
		return nil, fmt.Errorf("subscribe durable nats consumer %q: %w", cfg.Durable, err)
	}
	return &subscription{subscription: sub, conn: b.conn}, nil
}

func (b *DurableBroker) Close() error {
	if err := b.conn.Drain(); err != nil {
		b.conn.Close()
		return fmt.Errorf("drain durable nats connection: %w", err)
	}
	return nil
}

func (b *DurableBroker) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.conn.FlushWithContext(ctx); err != nil {
		return fmt.Errorf("ping durable nats connection: %w", err)
	}
	return nil
}

func validateMessage(message messaging.Message) error {
	if message.Subject == "" {
		return errors.New("publish durable nats message: subject is required")
	}
	return nil
}

func validateConsumer(cfg messaging.ConsumerConfig, handler messaging.Handler) error {
	switch {
	case cfg.Stream == "":
		return errors.New("subscribe durable nats consumer: stream is required")
	case cfg.Durable == "":
		return errors.New("subscribe durable nats consumer: durable name is required")
	case cfg.Subject == "":
		return errors.New("subscribe durable nats consumer: subject is required")
	case handler == nil:
		return errors.New("subscribe durable nats consumer: handler is required")
	default:
		return nil
	}
}

var _ messaging.DurableBroker = (*DurableBroker)(nil)

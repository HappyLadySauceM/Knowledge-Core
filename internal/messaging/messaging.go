package messaging

import (
	"context"
	"time"
)

type Message struct {
	ID          string
	Subject     string
	ContentType string
	Headers     map[string]string
	Body        []byte
}

type PublishOptions struct {
	DeduplicationID string
}

type ConsumerConfig struct {
	Stream            string
	Durable           string
	Queue             string
	Subject           string
	DeadLetterSubject string
	AckWait           time.Duration
	MaxDeliver        int
}

type Handler func(context.Context, Delivery)

type RealtimeHandler func(context.Context, string, []byte)

type DurableBroker interface {
	Publish(ctx context.Context, msg Message, opts PublishOptions) error
	Subscribe(ctx context.Context, cfg ConsumerConfig, handler Handler) (Subscription, error)
	Close() error
}

type Delivery interface {
	Message() Message
	Attempt() int
	Ack(ctx context.Context) error
	Nack(ctx context.Context, delay time.Duration) error
	Term(ctx context.Context, reason string) error
}

type RealtimeBus interface {
	Publish(ctx context.Context, subject string, payload []byte) error
	Subscribe(ctx context.Context, subject string, handler RealtimeHandler) (Subscription, error)
	Close() error
}

type Subscription interface {
	Stop(ctx context.Context) error
}

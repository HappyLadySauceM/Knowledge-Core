package nats

import (
	"context"
	"time"
)

// Message is the payload and NATS subject published through JetStream.
type Message struct {
	ID          string            `json:"id"`
	Subject     string            `json:"subject"`
	ContentType string            `json:"content_type,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        []byte            `json:"body"`
}

// PublishOptions controls JetStream-specific publish behavior.
type PublishOptions struct {
	// DeduplicationID is sent as JetStream's idempotency key. Message.ID is
	// used when this field is empty.
	DeduplicationID string
}

// ConsumerConfig configures a durable JetStream consumer.
type ConsumerConfig struct {
	Stream            string
	Durable           string
	Queue             string
	Subject           string
	DeadLetterSubject string
	AckWait           time.Duration
	MaxDeliver        int
}

// Handler processes one concrete JetStream delivery.
type Handler func(context.Context, *Delivery)

// RealtimeHandler processes one Core NATS message.
type RealtimeHandler func(context.Context, string, []byte)

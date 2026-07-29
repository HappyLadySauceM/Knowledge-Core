package nats

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/messaging"
	natsclient "github.com/nats-io/nats.go"
)

type delivery struct {
	js                natsclient.JetStreamContext
	msg               *natsclient.Msg
	message           messaging.Message
	attempt           int
	deadLetterSubject string
	settled           atomic.Bool
}

func newDelivery(js natsclient.JetStreamContext, msg *natsclient.Msg, deadLetterSubject string) *delivery {
	attempt := 1
	if metadata, err := msg.Metadata(); err == nil {
		attempt = int(metadata.NumDelivered)
	}
	message := messaging.Message{
		ID:          msg.Header.Get("X-Message-ID"),
		Subject:     msg.Subject,
		ContentType: msg.Header.Get("Content-Type"),
		Headers:     make(map[string]string, len(msg.Header)),
		Body:        append([]byte(nil), msg.Data...),
	}
	for key, values := range msg.Header {
		if len(values) > 0 {
			message.Headers[key] = values[0]
		}
	}
	return &delivery{js: js, msg: msg, message: message, attempt: attempt, deadLetterSubject: deadLetterSubject}
}

func (d *delivery) Message() messaging.Message {
	message := d.message
	message.Body = append([]byte(nil), d.message.Body...)
	message.Headers = make(map[string]string, len(d.message.Headers))
	for key, value := range d.message.Headers {
		message.Headers[key] = value
	}
	return message
}

func (d *delivery) Attempt() int { return d.attempt }

func (d *delivery) Ack(ctx context.Context) error {
	return d.settle(ctx, func() error { return d.msg.Ack() })
}

func (d *delivery) Nack(ctx context.Context, delay time.Duration) error {
	return d.settle(ctx, func() error {
		if delay > 0 {
			return d.msg.NakWithDelay(delay)
		}
		return d.msg.Nak()
	})
}

func (d *delivery) Term(ctx context.Context, reason string) error {
	return d.settle(ctx, func() error {
		if d.deadLetterSubject != "" {
			deadLetter := natsclient.NewMsg(d.deadLetterSubject)
			deadLetter.Data = append([]byte(nil), d.msg.Data...)
			for key, values := range d.msg.Header {
				deadLetter.Header[key] = append([]string(nil), values...)
			}
			deadLetter.Header.Set("X-Dead-Letter-Reason", reason)
			deadLetter.Header.Set("X-Original-Subject", d.msg.Subject)
			deadLetter.Header.Set("X-Delivery-Attempt", strconv.Itoa(d.attempt))
			if _, err := d.js.PublishMsg(deadLetter, natsclient.Context(ctx)); err != nil {
				return fmt.Errorf("publish nats dead letter: %w", err)
			}
		}
		if err := d.msg.Term(); err != nil {
			return fmt.Errorf("terminate nats delivery: %w", err)
		}
		return nil
	})
}

func (d *delivery) settle(ctx context.Context, action func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !d.settled.CompareAndSwap(false, true) {
		return fmt.Errorf("nats delivery already settled")
	}
	if err := action(); err != nil {
		d.settled.Store(false)
		return err
	}
	return nil
}

func (d *delivery) isSettled() bool { return d.settled.Load() }

var _ messaging.Delivery = (*delivery)(nil)

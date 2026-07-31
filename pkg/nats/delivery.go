package nats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	natsclient "github.com/nats-io/nats.go"
)

type jetStreamPublisher interface {
	PublishMsg(*natsclient.Msg, ...natsclient.PubOpt) (*natsclient.PubAck, error)
}

// Delivery is a concrete JetStream message delivery. It may be settled once
// through Ack, Nack, or Term.
type Delivery struct {
	js                jetStreamPublisher
	msg               *natsclient.Msg
	message           Message
	attempt           int
	deadLetterSubject string
	settled           atomic.Bool
}

func newDelivery(js jetStreamPublisher, msg *natsclient.Msg, deadLetterSubject string) *Delivery {
	attempt := 1
	if metadata, err := msg.Metadata(); err == nil {
		attempt = int(metadata.NumDelivered)
	}
	message := Message{
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
	return &Delivery{js: js, msg: msg, message: message, attempt: attempt, deadLetterSubject: deadLetterSubject}
}

func (d *Delivery) Message() Message {
	message := d.message
	message.Body = append([]byte(nil), d.message.Body...)
	message.Headers = make(map[string]string, len(d.message.Headers))
	for key, value := range d.message.Headers {
		message.Headers[key] = value
	}
	return message
}

func (d *Delivery) Attempt() int { return d.attempt }

func (d *Delivery) Ack(ctx context.Context) error {
	return d.settle(ctx, func() error {
		if err := d.msg.Ack(); err != nil {
			return fmt.Errorf("ack nats delivery: %w", err)
		}
		return nil
	})
}

func (d *Delivery) Nack(ctx context.Context, delay time.Duration) error {
	if delay < 0 {
		return errors.New("nack nats delivery: delay must be non-negative")
	}
	return d.settle(ctx, func() error {
		var err error
		if delay > 0 {
			err = d.msg.NakWithDelay(delay)
		} else {
			err = d.msg.Nak()
		}
		if err != nil {
			return fmt.Errorf("nack nats delivery: %w", err)
		}
		return nil
	})
}

func (d *Delivery) Term(ctx context.Context, reason string) error {
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
			injectTrace(ctx, deadLetter.Header)
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

func (d *Delivery) settle(ctx context.Context, action func() error) error {
	if ctx == nil {
		return errors.New("settle nats delivery: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !d.settled.CompareAndSwap(false, true) {
		return errors.New("nats delivery already settled")
	}
	if err := action(); err != nil {
		d.settled.Store(false)
		return err
	}
	return nil
}

func (d *Delivery) isSettled() bool { return d.settled.Load() }

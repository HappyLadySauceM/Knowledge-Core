package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
)

type Repository interface {
	ClaimOutbox(context.Context, int, time.Duration) ([]domain.OutboxMessage, error)
	MarkOutboxPublished(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
	ParkOutbox(context.Context, string) error
}

type Publisher interface {
	Publish(context.Context, natsresource.Message, natsresource.PublishOptions) error
}

type Worker struct {
	repository  Repository
	publisher   Publisher
	interval    time.Duration
	lease       time.Duration
	maxAttempts int
	logger      *slog.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	ready       atomic.Bool
	startOnce   sync.Once
	stopOnce    sync.Once
}

func New(ctx context.Context, repository Repository, publisher Publisher, interval, lease time.Duration, maxAttempts int, logger *slog.Logger) (*Worker, error) {
	if ctx == nil || repository == nil || publisher == nil || logger == nil || interval <= 0 || lease <= 0 || maxAttempts < 1 {
		return nil, errors.New("create platform outbox worker: valid dependencies and options are required")
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &Worker{repository: repository, publisher: publisher, interval: interval, lease: lease, maxAttempts: maxAttempts, logger: logger, ctx: runCtx, cancel: cancel, done: make(chan struct{})}, nil
}

func (w *Worker) Name() string { return "platform-config-outbox" }

func (w *Worker) Serve() error {
	started := false
	w.startOnce.Do(func() { started = true })
	if !started {
		return errors.New("platform outbox worker already started")
	}
	defer close(w.done)
	w.ready.Store(true)
	defer w.ready.Store(false)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.process(w.ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.WarnContext(w.ctx, "platform configuration outbox processing failed", slog.String("component", "platform.worker"), slog.String("error.type", fmt.Sprintf("%T", err)))
		}
		select {
		case <-w.ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) Ready(context.Context) error {
	if !w.ready.Load() {
		return errors.New("platform outbox worker is not running")
	}
	return nil
}

func (w *Worker) Shutdown(ctx context.Context) error {
	w.stopOnce.Do(w.cancel)
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) process(ctx context.Context) error {
	messages, err := w.repository.ClaimOutbox(coretrace.Suppress(ctx), 50, w.lease)
	if err != nil {
		return err
	}
	for _, message := range messages {
		workCtx := coretrace.ContextFromPropagation(ctx, message.Headers)
		err := w.publisher.Publish(workCtx, natsresource.Message{ID: message.ID, Subject: message.Subject, Headers: message.Headers, ContentType: "application/json", Body: message.Payload}, natsresource.PublishOptions{DeduplicationID: message.ID})
		if err == nil {
			if markErr := w.repository.MarkOutboxPublished(workCtx, message.ID); markErr != nil {
				return markErr
			}
			continue
		}
		if message.Attempts >= w.maxAttempts {
			if parkErr := w.repository.ParkOutbox(workCtx, message.ID); parkErr != nil {
				return errors.Join(err, parkErr)
			}
			continue
		}
		if retryErr := w.repository.RetryOutbox(workCtx, message.ID, backoff(message.Attempts)); retryErr != nil {
			return errors.Join(err, retryErr)
		}
	}
	return nil
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Second * time.Duration(1<<(attempt-1))
}

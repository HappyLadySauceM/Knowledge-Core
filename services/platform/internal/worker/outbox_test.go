package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
)

type fakeRepository struct {
	messages []domain.OutboxMessage
	marked   []string
	retried  []string
	parked   []string
	delay    time.Duration
}

func (f *fakeRepository) ClaimOutbox(context.Context, int, time.Duration) ([]domain.OutboxMessage, error) {
	return f.messages, nil
}
func (f *fakeRepository) MarkOutboxPublished(_ context.Context, id string) error {
	f.marked = append(f.marked, id)
	return nil
}
func (f *fakeRepository) RetryOutbox(_ context.Context, id string, delay time.Duration) error {
	f.retried = append(f.retried, id)
	f.delay = delay
	return nil
}
func (f *fakeRepository) ParkOutbox(_ context.Context, id string) error {
	f.parked = append(f.parked, id)
	return nil
}

type fakePublisher struct {
	err     error
	message natsresource.Message
	options natsresource.PublishOptions
}

func (f *fakePublisher) Publish(_ context.Context, message natsresource.Message, options natsresource.PublishOptions) error {
	f.message = message
	f.options = options
	return f.err
}

func newTestWorker(t *testing.T, repository Repository, publisher Publisher, maxAttempts int) *Worker {
	t.Helper()
	worker, err := New(context.Background(), repository, publisher, time.Second, 30*time.Second, maxAttempts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return worker
}

func TestProcessPublishesWithStableDeduplicationID(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{messages: []domain.OutboxMessage{{ID: "message-1", Subject: "platform.config.changed.v1", Payload: []byte(`{"revision":1}`), Attempts: 1}}}
	publisher := &fakePublisher{}
	if err := newTestWorker(t, repository, publisher, 8).process(context.Background()); err != nil {
		t.Fatalf("process() error = %v", err)
	}
	if len(repository.marked) != 1 || repository.marked[0] != "message-1" {
		t.Fatalf("marked = %#v", repository.marked)
	}
	if publisher.message.ID != "message-1" || publisher.options.DeduplicationID != "message-1" {
		t.Fatalf("published identifiers = (%q, %q)", publisher.message.ID, publisher.options.DeduplicationID)
	}
}

func TestProcessRetriesTransientPublishFailure(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{messages: []domain.OutboxMessage{{ID: "message-1", Attempts: 2}}}
	publisher := &fakePublisher{err: errors.New("broker unavailable")}
	if err := newTestWorker(t, repository, publisher, 8).process(context.Background()); err != nil {
		t.Fatalf("process() error = %v", err)
	}
	if len(repository.retried) != 1 || repository.delay != 2*time.Second || len(repository.parked) != 0 {
		t.Fatalf("retry = %#v delay = %s parked = %#v", repository.retried, repository.delay, repository.parked)
	}
}

func TestProcessParksExhaustedPublishFailure(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{messages: []domain.OutboxMessage{{ID: "message-1", Attempts: 8}}}
	publisher := &fakePublisher{err: errors.New("broker unavailable")}
	if err := newTestWorker(t, repository, publisher, 8).process(context.Background()); err != nil {
		t.Fatalf("process() error = %v", err)
	}
	if len(repository.parked) != 1 || repository.parked[0] != "message-1" || len(repository.retried) != 0 {
		t.Fatalf("parked = %#v retried = %#v", repository.parked, repository.retried)
	}
}

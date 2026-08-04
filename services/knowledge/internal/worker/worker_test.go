package worker

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

type outboxRetry struct {
	id    string
	delay time.Duration
}

type outboxRepositoryStub struct {
	messages       []domain.OutboxMessage
	claimContext   context.Context
	claimLimit     int
	claimLease     time.Duration
	markContexts   []context.Context
	markedIDs      []string
	retryContexts  []context.Context
	retries        []outboxRetry
	operationOrder *[]string
}

func (s *outboxRepositoryStub) ClaimOutbox(ctx context.Context, limit int, lease time.Duration) ([]domain.OutboxMessage, error) {
	s.claimContext = ctx
	s.claimLimit = limit
	s.claimLease = lease
	*s.operationOrder = append(*s.operationOrder, "claim")
	return append([]domain.OutboxMessage(nil), s.messages...), nil
}

func (s *outboxRepositoryStub) MarkOutboxPublished(ctx context.Context, id string) error {
	s.markContexts = append(s.markContexts, ctx)
	s.markedIDs = append(s.markedIDs, id)
	*s.operationOrder = append(*s.operationOrder, "mark")
	return nil
}

func (s *outboxRepositoryStub) RetryOutbox(ctx context.Context, id string, delay time.Duration) error {
	s.retryContexts = append(s.retryContexts, ctx)
	s.retries = append(s.retries, outboxRetry{id: id, delay: delay})
	*s.operationOrder = append(*s.operationOrder, "retry")
	return nil
}

func (*outboxRepositoryStub) QueueExpiredUploads(context.Context, int) error {
	panic("unexpected QueueExpiredUploads call")
}

func (*outboxRepositoryStub) ClaimAttachmentJobs(context.Context, int, time.Duration) ([]domain.ScanJob, error) {
	panic("unexpected ClaimAttachmentJobs call")
}

func (*outboxRepositoryStub) MarkAttachmentReady(context.Context, string, string) error {
	panic("unexpected MarkAttachmentReady call")
}

func (*outboxRepositoryStub) MarkAttachmentRejected(context.Context, string, string) error {
	panic("unexpected MarkAttachmentRejected call")
}

func (*outboxRepositoryStub) FinishAttachmentCleanup(context.Context, string, bool) error {
	panic("unexpected FinishAttachmentCleanup call")
}

func (*outboxRepositoryStub) RetryAttachmentJob(context.Context, string, string, int) error {
	panic("unexpected RetryAttachmentJob call")
}

func (*outboxRepositoryStub) ListPurgeCandidates(context.Context, int) ([]repository.PurgeCandidate, error) {
	panic("unexpected ListPurgeCandidates call")
}

func (*outboxRepositoryStub) PurgeDocument(context.Context, string) error {
	panic("unexpected PurgeDocument call")
}

func (*outboxRepositoryStub) PurgeMaintenanceData(context.Context) error {
	panic("unexpected PurgeMaintenanceData call")
}

type publishCall struct {
	ctx     context.Context
	message natsresource.Message
	options natsresource.PublishOptions
}

type publisherStub struct {
	err            error
	calls          []publishCall
	operationOrder *[]string
}

func (s *publisherStub) Publish(ctx context.Context, message natsresource.Message, options natsresource.PublishOptions) error {
	message.Body = append([]byte(nil), message.Body...)
	s.calls = append(s.calls, publishCall{ctx: ctx, message: message, options: options})
	*s.operationOrder = append(*s.operationOrder, "publish")
	return s.err
}

type outboxContextKey struct{}

func TestProcessOutboxMarksExactlyOnceAfterAcknowledgedPublish(t *testing.T) {
	operationTimeout := 9 * time.Second
	payload := []byte(`{"document_id":"document-1"}`)
	message := domain.OutboxMessage{
		ID: "outbox-1", Subject: "knowledge.permission.changed", Payload: payload, Attempts: 2,
	}
	order := make([]string, 0, 3)
	repository := &outboxRepositoryStub{messages: []domain.OutboxMessage{message}, operationOrder: &order}
	publisher := &publisherStub{operationOrder: &order}
	w := &Worker{
		options: config.WorkerOptions{OperationTimeout: operationTimeout}, repository: repository, publisher: publisher,
	}
	ctx, cancel := context.WithDeadline(
		context.WithValue(context.Background(), outboxContextKey{}, "request-context"),
		time.Now().Add(time.Minute),
	)
	t.Cleanup(cancel)

	if err := w.processOutbox(ctx); err != nil {
		t.Fatalf("processOutbox() error = %v", err)
	}

	if repository.claimLimit != 50 || repository.claimLease != operationTimeout {
		t.Fatalf("ClaimOutbox() = limit %d, lease %s", repository.claimLimit, repository.claimLease)
	}
	if len(publisher.calls) != 1 {
		t.Fatalf("Publish() calls = %d, want 1", len(publisher.calls))
	}
	call := publisher.calls[0]
	if call.message.ID != message.ID || call.message.Subject != message.Subject {
		t.Fatalf("Publish() message identity = %#v", call.message)
	}
	if call.message.ContentType != "application/json" {
		t.Fatalf("Publish() content type = %q, want application/json", call.message.ContentType)
	}
	if !bytes.Equal(call.message.Body, payload) {
		t.Fatalf("Publish() payload = %q, want %q", call.message.Body, payload)
	}
	if call.options.DeduplicationID != message.ID {
		t.Fatalf("Publish() deduplication ID = %q, want %q", call.options.DeduplicationID, message.ID)
	}
	if len(repository.markedIDs) != 1 || repository.markedIDs[0] != message.ID {
		t.Fatalf("MarkOutboxPublished() IDs = %v, want [%s]", repository.markedIDs, message.ID)
	}
	if len(repository.retries) != 0 {
		t.Fatalf("RetryOutbox() calls = %v, want none", repository.retries)
	}
	assertOperationOrder(t, order, []string{"claim", "publish", "mark"})
	assertContextPreserved(t, ctx, repository.claimContext, call.ctx, repository.markContexts[0])
}

func TestProcessOutboxRetriesPublishFailureWithoutMarking(t *testing.T) {
	tests := []struct {
		name       string
		publishErr error
	}{
		{name: "missing stream", publishErr: errors.New("no stream matches subject")},
		{name: "transient failure", publishErr: errors.New("publish request timed out")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := domain.OutboxMessage{
				ID: "outbox-2", Subject: "knowledge.permission.changed", Payload: []byte(`{"revision":2}`), Attempts: 4,
			}
			order := make([]string, 0, 3)
			repository := &outboxRepositoryStub{messages: []domain.OutboxMessage{message}, operationOrder: &order}
			publisher := &publisherStub{err: test.publishErr, operationOrder: &order}
			w := &Worker{
				options: config.WorkerOptions{OperationTimeout: 7 * time.Second}, repository: repository, publisher: publisher,
			}
			ctx, cancel := context.WithDeadline(
				context.WithValue(context.Background(), outboxContextKey{}, test.name),
				time.Now().Add(time.Minute),
			)
			t.Cleanup(cancel)

			if err := w.processOutbox(ctx); err != nil {
				t.Fatalf("processOutbox() error = %v", err)
			}

			if len(repository.markedIDs) != 0 {
				t.Fatalf("MarkOutboxPublished() IDs = %v, want none", repository.markedIDs)
			}
			if len(publisher.calls) != 1 {
				t.Fatalf("Publish() calls = %d, want 1", len(publisher.calls))
			}
			if len(repository.retries) != 1 {
				t.Fatalf("RetryOutbox() calls = %v, want 1", repository.retries)
			}
			if retry := repository.retries[0]; retry.id != message.ID || retry.delay != boundedBackoff(message.Attempts) {
				t.Fatalf("RetryOutbox() = %#v, want ID %q and delay %s", retry, message.ID, boundedBackoff(message.Attempts))
			}
			assertOperationOrder(t, order, []string{"claim", "publish", "retry"})
			assertContextPreserved(t, ctx, repository.claimContext, publisher.calls[0].ctx, repository.retryContexts[0])
		})
	}
}

func assertOperationOrder(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("operation order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("operation order = %v, want %v", got, want)
		}
	}
}

func assertContextPreserved(t *testing.T, want context.Context, contexts ...context.Context) {
	t.Helper()
	wantDeadline, wantHasDeadline := want.Deadline()
	for index, got := range contexts {
		if got == nil {
			t.Fatalf("context %d is nil", index)
		}
		if got != want {
			t.Errorf("context %d was replaced", index)
		}
		gotDeadline, gotHasDeadline := got.Deadline()
		if gotHasDeadline != wantHasDeadline || !gotDeadline.Equal(wantDeadline) {
			t.Errorf("context %d deadline = %v, %t; want %v, %t", index, gotDeadline, gotHasDeadline, wantDeadline, wantHasDeadline)
		}
		if got.Value(outboxContextKey{}) != want.Value(outboxContextKey{}) {
			t.Errorf("context %d value was not preserved", index)
		}
	}
}

var (
	_ Repository = (*outboxRepositoryStub)(nil)
	_ Publisher  = (*publisherStub)(nil)
)

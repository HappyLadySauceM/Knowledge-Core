package worker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
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

func (s *outboxRepositoryStub) ParkOutbox(context.Context, string, string) error { return nil }

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

type lifecycleRepositoryStub struct {
	mu          sync.Mutex
	claims      int
	secondClaim chan struct{}
	ops         []string
}

func (s *lifecycleRepositoryStub) ClaimOutbox(context.Context, int, time.Duration) ([]domain.OutboxMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	s.ops = append(s.ops, "outbox")
	if s.claims == 2 {
		close(s.secondClaim)
	}
	return nil, nil
}

func (*lifecycleRepositoryStub) MarkOutboxPublished(context.Context, string) error { return nil }
func (*lifecycleRepositoryStub) RetryOutbox(context.Context, string, time.Duration) error {
	return nil
}

func (*lifecycleRepositoryStub) ParkOutbox(context.Context, string, string) error { return nil }
func (s *lifecycleRepositoryStub) QueueExpiredUploads(context.Context, int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append(s.ops, "queue_expired")
	return nil
}
func (s *lifecycleRepositoryStub) ClaimAttachmentJobs(context.Context, int, time.Duration) ([]domain.ScanJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append(s.ops, "attachments")
	return nil, nil
}
func (*lifecycleRepositoryStub) MarkAttachmentReady(context.Context, string, string) error {
	return nil
}
func (*lifecycleRepositoryStub) MarkAttachmentRejected(context.Context, string, string) error {
	return nil
}
func (*lifecycleRepositoryStub) FinishAttachmentCleanup(context.Context, string, bool) error {
	return nil
}
func (*lifecycleRepositoryStub) RetryAttachmentJob(context.Context, string, string, int) error {
	return nil
}
func (s *lifecycleRepositoryStub) ListPurgeCandidates(context.Context, int) ([]repository.PurgeCandidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append(s.ops, "purge")
	return nil, nil
}
func (*lifecycleRepositoryStub) PurgeDocument(context.Context, string) error { return nil }
func (s *lifecycleRepositoryStub) PurgeMaintenanceData(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = append(s.ops, "maintenance")
	return nil
}

func (s *lifecycleRepositoryStub) snapshotOps() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ops...)
}

func (s *lifecycleRepositoryStub) resetOps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops = nil
}

type lifecycleObjectStoreStub struct{}

func (*lifecycleObjectStoreStub) OpenObject(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unexpected OpenObject call")
}
func (*lifecycleObjectStoreStub) RemoveObject(context.Context, string) error { return nil }

type lifecycleScannerStub struct{}

func (*lifecycleScannerStub) Scan(context.Context, io.Reader) (domain.ScanResult, error) {
	return domain.ScanResult{}, nil
}

type lifecyclePublisherStub struct{}

func (*lifecyclePublisherStub) Publish(context.Context, natsresource.Message, natsresource.PublishOptions) error {
	return nil
}

type lifecyclePurgerStub struct{}

func (*lifecyclePurgerStub) PurgeDocument(context.Context, string) error { return nil }

func (s *publisherStub) Publish(ctx context.Context, message natsresource.Message, options natsresource.PublishOptions) error {
	message.Body = append([]byte(nil), message.Body...)
	s.calls = append(s.calls, publishCall{ctx: ctx, message: message, options: options})
	*s.operationOrder = append(*s.operationOrder, "publish")
	return s.err
}

type outboxContextKey struct{}

type idleWakeSource struct{}

func (*idleWakeSource) Run(ctx context.Context, _ func(WakeKind)) error {
	<-ctx.Done()
	return nil
}

type channelWakeSource struct {
	wakes <-chan WakeKind
}

func (s *channelWakeSource) Run(ctx context.Context, emit func(WakeKind)) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case kind, ok := <-s.wakes:
			if !ok {
				<-ctx.Done()
				return nil
			}
			emit(kind)
		}
	}
}

func TestWorkerLifetimeIsOwnedByShutdown(t *testing.T) {
	startupCtx, cancelStartup := context.WithCancel(context.Background())
	repository := &lifecycleRepositoryStub{secondClaim: make(chan struct{})}
	worker, err := newWorker(
		startupCtx,
		config.WorkerOptions{PollInterval: time.Millisecond, OperationTimeout: time.Second},
		&idleWakeSource{},
		repository,
		&lifecycleObjectStoreStub{},
		&lifecycleScannerStub{},
		&lifecyclePublisherStub{},
		&lifecyclePurgerStub{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelStartup()

	serveDone := make(chan error, 1)
	go func() { serveDone <- worker.Serve() }()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := worker.Ready(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-repository.secondClaim:
	case err := <-serveDone:
		t.Fatalf("Serve() stopped with startup context: %v", err)
	case <-waitCtx.Done():
		t.Fatalf("worker did not continue polling: %v", waitCtx.Err())
	}

	if err := worker.Shutdown(waitCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-waitCtx.Done():
		t.Fatalf("Serve() did not stop: %v", waitCtx.Err())
	}
}

func TestWorkerWakeRunsSelectiveLanesAndCoalesces(t *testing.T) {
	repository := &lifecycleRepositoryStub{secondClaim: make(chan struct{})}
	w := &Worker{
		options:       config.WorkerOptions{OperationTimeout: time.Second},
		repository:    repository,
		objects:       &lifecycleObjectStoreStub{},
		scanner:       &lifecycleScannerStub{},
		publisher:     &lifecyclePublisherStub{},
		collaboration: &lifecyclePurgerStub{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		runContext:    context.Background(),
	}

	w.queueWake(WakeOutbox)
	w.queueWake(WakeOutbox)
	w.drainWake()
	assertOperationOrder(t, repository.snapshotOps(), []string{"outbox"})
	repository.resetOps()

	w.queueWake(WakeAttachment)
	w.drainWake()
	assertOperationOrder(t, repository.snapshotOps(), []string{"queue_expired", "attachments"})
	repository.resetOps()

	w.queueWake(WakeOutbox)
	w.queueWake(WakeAttachment)
	w.drainWake()
	assertOperationOrder(t, repository.snapshotOps(), []string{"outbox", "queue_expired", "attachments", "purge", "maintenance"})
	repository.resetOps()

	w.queueWake(WakeAll)
	w.drainWake()
	assertOperationOrder(t, repository.snapshotOps(), []string{"outbox", "queue_expired", "attachments", "purge", "maintenance"})
}

func TestWorkerWakeSourceTriggersOutboxLane(t *testing.T) {
	wakes := make(chan WakeKind, 1)
	repository := &lifecycleRepositoryStub{secondClaim: make(chan struct{})}
	worker, err := newWorker(
		context.Background(),
		config.WorkerOptions{PollInterval: time.Hour, OperationTimeout: time.Second},
		&channelWakeSource{wakes: wakes},
		repository,
		&lifecycleObjectStoreStub{},
		&lifecycleScannerStub{},
		&lifecyclePublisherStub{},
		&lifecyclePurgerStub{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- worker.Serve() }()
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Shutdown(shutdownCtx)
		<-serveDone
	})

	waitCtx, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if err := worker.Ready(waitCtx); err != nil {
		t.Fatal(err)
	}
	waitForOps(t, repository, "outbox", "queue_expired", "attachments", "purge", "maintenance")
	baseline := len(repository.snapshotOps())

	wakes <- WakeOutbox
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ops := repository.snapshotOps()
		if len(ops) > baseline && ops[len(ops)-1] == "outbox" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ops after wake = %v, want trailing outbox", repository.snapshotOps())
}

func TestProcessOutboxMarksExactlyOnceAfterAcknowledgedPublish(t *testing.T) {
	operationTimeout := 9 * time.Second
	payload := []byte(`{"document_id":"document-1"}`)
	message := domain.OutboxMessage{
		ID: "outbox-1", Subject: "knowledge.permission.changed", Payload: payload, Attempts: 2,
		Headers: map[string]string{"traceparent": "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"},
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
	if !coretrace.IsSuppressed(repository.claimContext) {
		t.Fatal("ClaimOutbox() context should be suppressed for empty-poll noise control")
	}
	if len(publisher.calls) != 1 {
		t.Fatalf("Publish() calls = %d, want 1", len(publisher.calls))
	}
	call := publisher.calls[0]
	if coretrace.IsSuppressed(call.ctx) {
		t.Fatal("Publish() context must not inherit claim suppression")
	}
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
	assertWorkContextPreserved(t, ctx, call.ctx, repository.markContexts[0])
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

			if !coretrace.IsSuppressed(repository.claimContext) {
				t.Fatal("ClaimOutbox() context should be suppressed")
			}
			if len(repository.markedIDs) != 0 {
				t.Fatalf("MarkOutboxPublished() IDs = %v, want none", repository.markedIDs)
			}
			if len(publisher.calls) != 1 {
				t.Fatalf("Publish() calls = %d, want 1", len(publisher.calls))
			}
			if coretrace.IsSuppressed(publisher.calls[0].ctx) {
				t.Fatal("Publish() context must not be suppressed")
			}
			if len(repository.retries) != 1 {
				t.Fatalf("RetryOutbox() calls = %v, want 1", repository.retries)
			}
			if retry := repository.retries[0]; retry.id != message.ID || retry.delay != boundedBackoff(message.Attempts) {
				t.Fatalf("RetryOutbox() = %#v, want ID %q and delay %s", retry, message.ID, boundedBackoff(message.Attempts))
			}
			assertOperationOrder(t, order, []string{"claim", "publish", "retry"})
			assertWorkContextPreserved(t, ctx, publisher.calls[0].ctx, repository.retryContexts[0])
		})
	}
}

func TestParseWakePayload(t *testing.T) {
	t.Parallel()
	if got := parseWakePayload(repository.WorkerWakePayloadOutbox); got != WakeOutbox {
		t.Fatalf("parseWakePayload(outbox) = %v", got)
	}
	if got := parseWakePayload(repository.WorkerWakePayloadAttachment); got != WakeAttachment {
		t.Fatalf("parseWakePayload(attachment) = %v", got)
	}
	if got := parseWakePayload("purge"); got != 0 {
		t.Fatalf("parseWakePayload(purge) = %v, want 0", got)
	}
}

func waitForOps(t *testing.T, repository *lifecycleRepositoryStub, want ...string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		ops := repository.snapshotOps()
		if hasPrefixOps(ops, want...) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ops = %v, want prefix %v", repository.snapshotOps(), want)
}

func hasPrefixOps(got []string, want ...string) bool {
	if len(got) < len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
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

func assertWorkContextPreserved(t *testing.T, parent context.Context, contexts ...context.Context) {
	t.Helper()
	wantDeadline, wantHasDeadline := parent.Deadline()
	for index, got := range contexts {
		if got == nil {
			t.Fatalf("context %d is nil", index)
		}
		if coretrace.IsSuppressed(got) {
			t.Errorf("context %d is suppressed", index)
		}
		gotDeadline, gotHasDeadline := got.Deadline()
		if gotHasDeadline != wantHasDeadline || !gotDeadline.Equal(wantDeadline) {
			t.Errorf("context %d deadline = %v, %t; want %v, %t", index, gotDeadline, gotHasDeadline, wantDeadline, wantHasDeadline)
		}
		if got.Value(outboxContextKey{}) != parent.Value(outboxContextKey{}) {
			t.Errorf("context %d value was not preserved", index)
		}
	}
}

var (
	_ Repository = (*outboxRepositoryStub)(nil)
	_ Publisher  = (*publisherStub)(nil)
	_ WakeSource = (*idleWakeSource)(nil)
	_ WakeSource = (*channelWakeSource)(nil)
)

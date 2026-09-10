package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const componentName = "knowledge-background-workers"

type Repository interface {
	ClaimOutbox(context.Context, int, time.Duration) ([]domain.OutboxMessage, error)
	MarkOutboxPublished(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
	ParkOutbox(context.Context, string, string) error
	ClaimPublicationReferenceJobs(context.Context, int, time.Duration) ([]domain.PublicationReferenceJob, error)
	MarkPublicationReferencesStaged(context.Context, string) error
	PromotePublication(context.Context, domain.PublicationReferenceJob) error
	CompletePublicationReferenceJob(context.Context, string, bool) error
	RetryPublicationReferenceJob(context.Context, string, string, int) error
	ParkPublicationReferenceJob(context.Context, string, string, bool) error
	ListPurgeCandidates(context.Context, int) ([]repository.PurgeCandidate, error)
	PurgeDocument(context.Context, string) error
	PurgeMaintenanceData(context.Context) error
}

type PublicationReferences interface {
	Stage(context.Context, domain.PublicationReferenceJob) error
	Finalize(context.Context, domain.PublicationReferenceJob) error
	Clear(context.Context, domain.PublicationReferenceJob) error
}

type Publisher interface {
	Publish(context.Context, natsresource.Message, natsresource.PublishOptions) error
}

type DocumentPurger interface {
	PurgeDocument(context.Context, string) error
}

type Worker struct {
	options       config.WorkerOptions
	dynamic       atomic.Pointer[config.WorkerOptions]
	repository    Repository
	attachments   PublicationReferences
	publisher     Publisher
	collaboration DocumentPurger
	wakeSource    WakeSource
	logger        *slog.Logger
	runContext    context.Context
	cancel        context.CancelFunc
	ready         chan struct{}
	done          chan struct{}
	started       atomic.Bool
	readyOnce     sync.Once
	pendingMu     sync.Mutex
	pendingWake   WakeKind
}

func (w *Worker) handlePublicationReferenceJobForTest(ctx context.Context, repository interface {
	MarkPublicationReferencesStaged(context.Context, string) error
	PromotePublication(context.Context, domain.PublicationReferenceJob) error
	CompletePublicationReferenceJob(context.Context, string, bool) error
	RetryPublicationReferenceJob(context.Context, string, string, int) error
	ParkPublicationReferenceJob(context.Context, string, string, bool) error
}, job domain.PublicationReferenceJob) error {
	return w.handlePublicationReferenceJobWithRepository(ctx, repository, job)
}

func New(
	ctx context.Context,
	options config.WorkerOptions,
	db *sql.DB,
	repository Repository,
	attachments PublicationReferences,
	publisher Publisher,
	collaboration DocumentPurger,
	logger *slog.Logger,
) (*Worker, error) {
	wakeSource, err := newPostgresWakeSource(db, logger)
	if err != nil {
		return nil, fmt.Errorf("create knowledge workers: %w", err)
	}
	return newWorker(ctx, options, wakeSource, repository, attachments, publisher, collaboration, logger)
}

func newWorker(
	ctx context.Context,
	options config.WorkerOptions,
	wakeSource WakeSource,
	repository Repository,
	attachments PublicationReferences,
	publisher Publisher,
	collaboration DocumentPurger,
	logger *slog.Logger,
) (*Worker, error) {
	if ctx == nil || wakeSource == nil || repository == nil || attachments == nil || publisher == nil || collaboration == nil || logger == nil {
		return nil, errors.New("create knowledge workers: context and dependencies are required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create knowledge workers: %w", err)
	}
	// Register is bounded by the application startup deadline. The worker owns
	// its longer lifetime and is stopped explicitly through Shutdown.
	runContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	worker := &Worker{
		options: options, repository: repository, attachments: attachments,
		publisher: publisher, collaboration: collaboration, wakeSource: wakeSource, logger: logger,
		runContext: runContext, cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}),
	}
	worker.SetOptions(options)
	return worker, nil
}

func (w *Worker) Name() string { return componentName }

func (w *Worker) Serve() error {
	if w == nil || !w.started.CompareAndSwap(false, true) {
		return errors.New("serve knowledge workers: component is nil or already started")
	}
	w.readyOnce.Do(func() { close(w.ready) })
	defer close(w.done)

	signal := make(chan struct{}, 1)
	var listenDone sync.WaitGroup
	listenDone.Add(1)
	go func() {
		defer listenDone.Done()
		if err := w.wakeSource.Run(w.runContext, func(kind WakeKind) {
			w.queueWake(kind)
			select {
			case signal <- struct{}{}:
			default:
			}
		}); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.WarnContext(w.runContext, "knowledge worker wake source stopped",
				slog.String("component", "knowledge.worker"),
				slog.String("event", "wake_source_stopped"),
				slog.String("error.type", fmt.Sprintf("%T", err)),
			)
		}
	}()
	defer listenDone.Wait()

	w.runOnce()
	ticker := time.NewTicker(w.currentOptions().PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.runContext.Done():
			return nil
		case <-ticker.C:
			w.runOnce()
			ticker.Reset(w.currentOptions().PollInterval)
		case <-signal:
			w.drainWake()
		}
	}
}

func (w *Worker) SetOptions(options config.WorkerOptions) {
	if w != nil {
		copy := options
		w.dynamic.Store(&copy)
	}
}

func (w *Worker) currentOptions() config.WorkerOptions {
	if options := w.dynamic.Load(); options != nil {
		return *options
	}
	return w.options
}

func (w *Worker) queueWake(kind WakeKind) {
	if kind == 0 {
		return
	}
	w.pendingMu.Lock()
	w.pendingWake |= kind
	w.pendingMu.Unlock()
}

func (w *Worker) drainWake() {
	w.pendingMu.Lock()
	kind := w.pendingWake
	w.pendingWake = 0
	w.pendingMu.Unlock()
	if kind == 0 {
		return
	}
	if kind&WakeAll != 0 || kind&(WakeOutbox|WakePublication) == (WakeOutbox|WakePublication) {
		w.runOnce()
		return
	}
	if kind&WakeOutbox != 0 {
		w.runBounded("outbox", w.processOutbox)
	}
	if kind&WakePublication != 0 {
		w.runBounded("publication", w.processPublicationReferences)
	}
}

func (w *Worker) Ready(ctx context.Context) error {
	if w == nil {
		return errors.New("wait for knowledge workers: component is nil")
	}
	select {
	case <-w.ready:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for knowledge workers: %w", ctx.Err())
	}
}

func (w *Worker) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.cancel()
	if !w.started.Load() {
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop knowledge workers: %w", ctx.Err())
	}
}

func (w *Worker) runOnce() {
	w.runBounded("outbox", w.processOutbox)
	w.runBounded("publication", w.processPublicationReferences)
	w.runBounded("purge", w.processPurge)
}

func (w *Worker) runBounded(operation string, run func(context.Context) error) {
	ctx, cancel := context.WithTimeout(w.runContext, w.currentOptions().OperationTimeout)
	defer cancel()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		w.logger.WarnContext(ctx, "knowledge background operation failed",
			slog.String("component", "knowledge.worker"),
			slog.String("event", "operation_failed"),
			slog.String("operation", operation),
			slog.String("error.type", fmt.Sprintf("%T", err)),
		)
	}
}

func (w *Worker) processOutbox(ctx context.Context) error {
	probeCtx := coretrace.Suppress(ctx)
	messages, err := w.repository.ClaimOutbox(probeCtx, 50, w.currentOptions().OperationTimeout)
	if err != nil {
		return err
	}
	for _, message := range messages {
		workCtx := coretrace.ContextFromPropagation(ctx, message.Headers)
		if err := w.publisher.Publish(workCtx, natsresource.Message{
			ID:          message.ID,
			Subject:     message.Subject,
			Headers:     message.Headers,
			ContentType: "application/json",
			Body:        message.Payload,
		}, natsresource.PublishOptions{DeduplicationID: message.ID}); err != nil {
			if message.Attempts >= 8 {
				if parkErr := w.repository.ParkOutbox(workCtx, message.ID, "publish_retry_exhausted"); parkErr != nil {
					return errors.Join(err, parkErr)
				}
				continue
			}
			if retryErr := w.repository.RetryOutbox(workCtx, message.ID, boundedBackoff(message.Attempts)); retryErr != nil {
				return errors.Join(err, retryErr)
			}
			continue
		}
		if err := w.repository.MarkOutboxPublished(workCtx, message.ID); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processPublicationReferences(ctx context.Context) error {
	probeCtx := coretrace.Suppress(ctx)
	jobs, err := w.repository.ClaimPublicationReferenceJobs(probeCtx, 10, w.currentOptions().OperationTimeout)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := w.handlePublicationReferenceJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) handlePublicationReferenceJob(ctx context.Context, job domain.PublicationReferenceJob) error {
	return w.handlePublicationReferenceJobWithRepository(ctx, w.repository, job)
}

func (w *Worker) handlePublicationReferenceJobWithRepository(ctx context.Context, repository interface {
	MarkPublicationReferencesStaged(context.Context, string) error
	PromotePublication(context.Context, domain.PublicationReferenceJob) error
	CompletePublicationReferenceJob(context.Context, string, bool) error
	RetryPublicationReferenceJob(context.Context, string, string, int) error
	ParkPublicationReferenceJob(context.Context, string, string, bool) error
}, job domain.PublicationReferenceJob) error {
	workCtx := coretrace.ContextFromPropagation(ctx, job.Headers)
	workCtx, span := otel.Tracer("knowledge-core/knowledge").Start(workCtx, "knowledge.worker.publication_references",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal))
	defer span.End()
	var err error
	if job.Action == "clear" {
		err = w.attachments.Clear(workCtx, job)
		if err == nil {
			err = repository.CompletePublicationReferenceJob(workCtx, job.ID, true)
		}
	} else {
		if job.State == "pending" {
			err = w.attachments.Stage(workCtx, job)
			if err == nil {
				err = repository.MarkPublicationReferencesStaged(workCtx, job.ID)
			}
			if err == nil {
				job.State = "staged"
			}
		}
		if err == nil && job.State == "staged" {
			err = repository.PromotePublication(workCtx, job)
			if err == nil {
				job.State = "promoted"
			}
		}
		if err == nil && job.State == "promoted" {
			err = w.attachments.Finalize(workCtx, job)
			if err == nil {
				err = repository.CompletePublicationReferenceJob(workCtx, job.ID, false)
			}
		}
		if err == nil && job.State != "promoted" {
			err = fmt.Errorf("unknown publication job state %q", job.State)
		}
	}
	if err == nil {
		return nil
	}
	if job.Attempts >= 8 {
		if parkErr := repository.ParkPublicationReferenceJob(workCtx, job.ID, "publication_reference_retry_exhausted", job.Action == "publish"); parkErr != nil {
			return errors.Join(err, parkErr)
		}
		return nil
	}
	if retryErr := repository.RetryPublicationReferenceJob(workCtx, job.ID, "publication_reference_unavailable", job.Attempts); retryErr != nil {
		return errors.Join(err, retryErr)
	}
	return nil
}

func (w *Worker) processPurge(ctx context.Context) error {
	probeCtx := coretrace.Suppress(ctx)
	candidates, err := w.repository.ListPurgeCandidates(probeCtx, 20)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		workCtx, span := otel.Tracer("knowledge-core/knowledge").Start(ctx, "knowledge.worker.purge_document",
			oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		)
		if err := w.collaboration.PurgeDocument(workCtx, candidate.DocumentID); err != nil {
			span.End()
			return err
		}
		if err := w.repository.PurgeDocument(workCtx, candidate.DocumentID); err != nil {
			span.End()
			return err
		}
		span.End()
	}
	return w.repository.PurgeMaintenanceData(probeCtx)
}

func boundedBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}

package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

const componentName = "knowledge-background-workers"

type Repository interface {
	ClaimOutbox(context.Context, int, time.Duration) ([]domain.OutboxMessage, error)
	MarkOutboxPublished(context.Context, string) error
	RetryOutbox(context.Context, string, time.Duration) error
	QueueExpiredUploads(context.Context, int) error
	ClaimAttachmentJobs(context.Context, int, time.Duration) ([]domain.ScanJob, error)
	MarkAttachmentReady(context.Context, string, string) error
	MarkAttachmentRejected(context.Context, string, string) error
	FinishAttachmentCleanup(context.Context, string, bool) error
	RetryAttachmentJob(context.Context, string, string, int) error
	ListPurgeCandidates(context.Context, int) ([]repository.PurgeCandidate, error)
	PurgeDocument(context.Context, string) error
	PurgeMaintenanceData(context.Context) error
}

type ObjectStore interface {
	OpenObject(context.Context, string) (io.ReadCloser, error)
	RemoveObject(context.Context, string) error
}

type Scanner interface {
	Scan(context.Context, io.Reader) (domain.ScanResult, error)
}

type Publisher interface {
	Publish(context.Context, natsresource.Message, natsresource.PublishOptions) error
}

type DocumentPurger interface {
	PurgeDocument(context.Context, string) error
}

type Worker struct {
	options       config.WorkerOptions
	repository    Repository
	objects       ObjectStore
	scanner       Scanner
	publisher     Publisher
	collaboration DocumentPurger
	logger        *slog.Logger
	runContext    context.Context
	cancel        context.CancelFunc
	ready         chan struct{}
	done          chan struct{}
	started       atomic.Bool
	readyOnce     sync.Once
}

func New(
	ctx context.Context,
	options config.WorkerOptions,
	repository Repository,
	objects ObjectStore,
	scanner Scanner,
	publisher Publisher,
	collaboration DocumentPurger,
	logger *slog.Logger,
) (*Worker, error) {
	if ctx == nil || repository == nil || objects == nil || scanner == nil || publisher == nil || collaboration == nil || logger == nil {
		return nil, errors.New("create knowledge workers: context and dependencies are required")
	}
	if err := options.Validate(); err != nil {
		return nil, fmt.Errorf("create knowledge workers: %w", err)
	}
	runContext, cancel := context.WithCancel(ctx)
	return &Worker{
		options: options, repository: repository, objects: objects, scanner: scanner,
		publisher: publisher, collaboration: collaboration, logger: logger, runContext: runContext,
		cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (w *Worker) Name() string { return componentName }

func (w *Worker) Serve() error {
	if w == nil || !w.started.CompareAndSwap(false, true) {
		return errors.New("serve knowledge workers: component is nil or already started")
	}
	w.readyOnce.Do(func() { close(w.ready) })
	defer close(w.done)
	w.runOnce()
	ticker := time.NewTicker(w.options.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-w.runContext.Done():
			return nil
		case <-ticker.C:
			w.runOnce()
		}
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
	w.runBounded("attachments", w.processAttachments)
	w.runBounded("purge", w.processPurge)
}

func (w *Worker) runBounded(operation string, run func(context.Context) error) {
	ctx, cancel := context.WithTimeout(w.runContext, w.options.OperationTimeout)
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
	messages, err := w.repository.ClaimOutbox(ctx, 50, w.options.OperationTimeout)
	if err != nil {
		return err
	}
	for _, message := range messages {
		if err := w.publisher.Publish(ctx, natsresource.Message{
			ID:          message.ID,
			Subject:     message.Subject,
			ContentType: "application/json",
			Body:        message.Payload,
		}, natsresource.PublishOptions{DeduplicationID: message.ID}); err != nil {
			if retryErr := w.repository.RetryOutbox(ctx, message.ID, boundedBackoff(message.Attempts)); retryErr != nil {
				return errors.Join(err, retryErr)
			}
			continue
		}
		if err := w.repository.MarkOutboxPublished(ctx, message.ID); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) processAttachments(ctx context.Context) error {
	if err := w.repository.QueueExpiredUploads(ctx, 100); err != nil {
		return err
	}
	jobs, err := w.repository.ClaimAttachmentJobs(ctx, 10, w.options.OperationTimeout)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		attachment := job.Attachment
		switch attachment.Status {
		case domain.AttachmentScanning:
			if err := w.scanAttachment(ctx, job); err != nil {
				if retryErr := w.repository.RetryAttachmentJob(ctx, attachment.ID, "scan_unavailable", job.Attempts); retryErr != nil {
					return errors.Join(err, retryErr)
				}
			}
		case domain.AttachmentRejected:
			if err := w.objects.RemoveObject(ctx, attachment.ObjectKey); err != nil {
				if retryErr := w.repository.RetryAttachmentJob(ctx, attachment.ID, "cleanup_unavailable", job.Attempts); retryErr != nil {
					return errors.Join(err, retryErr)
				}
				continue
			}
			if err := w.repository.FinishAttachmentCleanup(ctx, attachment.ID, false); err != nil {
				return err
			}
		case domain.AttachmentDeleting:
			if err := w.objects.RemoveObject(ctx, attachment.ObjectKey); err != nil {
				if retryErr := w.repository.RetryAttachmentJob(ctx, attachment.ID, "delete_unavailable", job.Attempts); retryErr != nil {
					return errors.Join(err, retryErr)
				}
				continue
			}
			if err := w.repository.FinishAttachmentCleanup(ctx, attachment.ID, true); err != nil {
				return err
			}
		default:
			if err := w.repository.FinishAttachmentCleanup(ctx, attachment.ID, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) scanAttachment(ctx context.Context, job domain.ScanJob) error {
	reader, err := w.objects.OpenObject(ctx, job.Attachment.ObjectKey)
	if err != nil {
		return err
	}
	result, scanErr := w.scanner.Scan(ctx, reader)
	closeErr := reader.Close()
	if scanErr != nil || closeErr != nil {
		return errors.Join(scanErr, closeErr)
	}
	if !result.Clean || strings.HasPrefix(job.Attachment.DeclaredType, "image/") && !strings.HasPrefix(result.DetectedType, "image/") {
		return w.repository.MarkAttachmentRejected(ctx, job.Attachment.ID, "content_rejected")
	}
	return w.repository.MarkAttachmentReady(ctx, job.Attachment.ID, result.DetectedType)
}

func (w *Worker) processPurge(ctx context.Context) error {
	candidates, err := w.repository.ListPurgeCandidates(ctx, 20)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := w.collaboration.PurgeDocument(ctx, candidate.DocumentID); err != nil {
			return err
		}
		for _, objectKey := range candidate.ObjectKeys {
			if err := w.objects.RemoveObject(ctx, objectKey); err != nil {
				return err
			}
		}
		if err := w.repository.PurgeDocument(ctx, candidate.DocumentID); err != nil {
			return err
		}
	}
	return w.repository.PurgeMaintenanceData(ctx)
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

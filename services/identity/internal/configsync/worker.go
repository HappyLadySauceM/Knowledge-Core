package configsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform/platformservice"
	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	natsresource "github.com/HappyLadySauce/Knowledge-Core/pkg/nats"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/config"
	identityemail "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/email"
	"github.com/google/uuid"
)

type event struct {
	MessageID string `json:"message_id"`
	Namespace string `json:"namespace"`
	Revision  int64  `json:"aggregate_version"`
}

type Worker struct {
	broker       *natsresource.DurableBroker
	platform     platformservice.Client
	email        *identityemail.Worker
	serviceToken string
	logger       *slog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	sub          *natsresource.Subscription
	ready        atomic.Bool
	lastApplied  atomic.Int64
	startOnce    sync.Once
	stopOnce     sync.Once
	done         chan struct{}
}

func New(ctx context.Context, broker *natsresource.DurableBroker, platform platformservice.Client, email *identityemail.Worker, serviceToken string, logger *slog.Logger) (*Worker, error) {
	if ctx == nil || broker == nil || platform == nil || email == nil || serviceToken == "" || logger == nil {
		return nil, errors.New("create identity configuration sync worker: dependencies are required")
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &Worker{broker: broker, platform: platform, email: email, serviceToken: serviceToken, logger: logger, ctx: runCtx, cancel: cancel, done: make(chan struct{})}, nil
}

func (w *Worker) Name() string { return "identity-configuration-sync" }

func (w *Worker) Serve() error {
	started := false
	w.startOnce.Do(func() { started = true })
	if !started {
		return errors.New("identity configuration sync worker already started")
	}
	defer close(w.done)
	sub, err := w.broker.Subscribe(w.ctx, natsresource.ConsumerConfig{
		Stream: "KNOWLEDGE_CORE_CONFIG", Durable: "identity-email-config-v1", Subject: "platform.config.changed.v1",
		DeadLetterSubject: "platform.config.dead.identity", AckWait: 2 * time.Minute, MaxDeliver: 8,
	}, w.handle)
	if err != nil {
		return fmt.Errorf("subscribe identity configuration events: %w", err)
	}
	w.sub = sub
	// Reconcile once on startup so a restart or a lost event cannot leave the
	// process on an old last-good configuration indefinitely.
	if err := w.reconcile(w.ctx); err != nil {
		w.logger.WarnContext(w.ctx, "identity configuration startup reconciliation deferred", slog.String("error.type", fmt.Sprintf("%T", err)))
	}
	w.ready.Store(true)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			w.ready.Store(false)
			return nil
		case <-ticker.C:
			if err := w.reconcile(w.ctx); err != nil {
				w.logger.WarnContext(w.ctx, "identity configuration reconciliation failed", slog.String("error.type", fmt.Sprintf("%T", err)))
			}
		}
	}
}

func (w *Worker) Ready(context.Context) error {
	if !w.ready.Load() {
		return errors.New("identity configuration sync worker is not running")
	}
	return nil
}

func (w *Worker) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(w.cancel)
	var stopErr error
	if w.sub != nil {
		stopErr = w.sub.Stop(ctx)
	}
	select {
	case <-w.done:
		return stopErr
	case <-ctx.Done():
		return errors.Join(stopErr, ctx.Err())
	}
}

func (w *Worker) handle(ctx context.Context, delivery *natsresource.Delivery) {
	message := delivery.Message()
	var notification event
	if err := json.Unmarshal(message.Body, &notification); err != nil {
		_ = delivery.Term(ctx, "invalid_configuration_event")
		return
	}
	if notification.Namespace != "email" {
		_ = delivery.Ack(ctx)
		return
	}
	messageID := notification.MessageID
	if messageID == "" {
		messageID = message.ID
	}
	if messageID == "" {
		messageID = message.Headers["X-Message-ID"]
	}
	if notification.Revision <= 0 {
		if value, parseErr := strconv.ParseInt(message.Headers["X-Aggregate-Version"], 10, 64); parseErr == nil {
			notification.Revision = value
		}
	}
	if notification.Revision <= 0 || messageID == "" {
		_ = delivery.Term(ctx, "invalid_configuration_event")
		return
	}
	if err := w.report(ctx, messageID, notification.Revision, "validating", delivery.Attempt(), ""); err != nil {
		_ = delivery.Nack(ctx, retryDelay(delivery.Attempt()))
		return
	}
	configuration, err := w.platform.GetConsumerConfiguration(coreauth.WithServiceToken(ctx, w.serviceToken), &platformv1.GetConsumerConfigurationRequest{Namespace: "email", Revision: notification.Revision, Consumer: "identity.email"})
	if err == nil {
		candidate, parseErr := smtpOptions(configuration)
		if parseErr != nil {
			err = parseErr
		} else {
			err = w.email.ApplyConfig(ctx, candidate)
		}
	}
	if err == nil {
		if reportErr := w.report(ctx, messageID, notification.Revision, "applied", delivery.Attempt(), ""); reportErr != nil {
			_ = delivery.Nack(ctx, retryDelay(delivery.Attempt()))
			return
		}
		_ = delivery.Ack(ctx)
		return
	}
	if delivery.Attempt() >= 8 {
		_ = w.report(ctx, messageID, notification.Revision, "parked", delivery.Attempt(), "smtp_validation_failed")
		_ = delivery.Term(ctx, "smtp_validation_failed")
		return
	}
	if reportErr := w.report(ctx, messageID, notification.Revision, "retrying", delivery.Attempt(), "smtp_validation_failed"); reportErr != nil {
		_ = delivery.Nack(ctx, retryDelay(delivery.Attempt()))
		return
	}
	_ = delivery.Nack(ctx, retryDelay(delivery.Attempt()))
}

func (w *Worker) reconcile(ctx context.Context) error {
	state, err := w.platform.GetConsumerState(coreauth.WithServiceToken(ctx, w.serviceToken), &platformv1.GetConsumerStateRequest{Namespace: "email", Consumer: "identity.email"})
	if err != nil {
		return fmt.Errorf("read identity configuration consumer state: %w", err)
	}
	if state == nil || state.DesiredRevision <= 0 || state.DesiredRevision <= state.AppliedRevision {
		return nil
	}
	revision := state.DesiredRevision
	configuration, err := w.platform.GetConsumerConfiguration(coreauth.WithServiceToken(ctx, w.serviceToken), &platformv1.GetConsumerConfigurationRequest{Namespace: "email", Revision: revision, Consumer: "identity.email"})
	if err != nil {
		return fmt.Errorf("read identity desired configuration revision: %w", err)
	}
	candidate, err := smtpOptions(configuration)
	if err != nil {
		return err
	}
	messageID := uuid.NewString()
	if err := w.report(ctx, messageID, revision, "validating", 0, ""); err != nil {
		return fmt.Errorf("report identity configuration validation: %w", err)
	}
	if err := w.email.ApplyConfig(ctx, candidate); err != nil {
		_ = w.report(ctx, messageID, revision, "retrying", 0, "smtp_validation_failed")
		return fmt.Errorf("apply identity desired configuration: %w", err)
	}
	if err := w.report(ctx, messageID, revision, "applied", 0, ""); err != nil {
		return fmt.Errorf("report identity configuration applied: %w", err)
	}
	w.lastApplied.Store(revision)
	return nil
}

func (w *Worker) report(ctx context.Context, messageID string, revision int64, status string, attempts int, errorKey string) error {
	var lastError *string
	if errorKey != "" {
		lastError = &errorKey
	}
	_, err := w.platform.ReportConfigurationApply(coreauth.WithServiceToken(ctx, w.serviceToken), &platformv1.ReportConfigurationApplyRequest{MessageId: messageID, Namespace: "email", Revision: revision, Consumer: "identity.email", Status: status, Attempts: int32(attempts), LastErrorKey: lastError})
	return err
}

func smtpOptions(configuration *platformv1.Configuration) (config.SMTPOptions, error) {
	if configuration == nil || configuration.Namespace != "email" {
		return config.SMTPOptions{}, errors.New("configuration namespace is not email")
	}
	values := make(map[string]string, len(configuration.Values))
	for _, value := range configuration.Values {
		if value == nil {
			continue
		}
		values[value.Key] = value.Value
	}
	port, err := strconv.Atoi(values["port"])
	if err != nil {
		return config.SMTPOptions{}, fmt.Errorf("parse SMTP port: %w", err)
	}
	return config.SMTPOptions{Host: values["host"], Port: port, Username: values["username"], Password: values["password"], From: values["from"], FrontendBaseURL: values["frontend_base_url"]}, nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 4 {
		attempt = 4
	}
	return time.Duration(1<<(attempt-1)) * 10 * time.Second
}

var _ coreapp.Component = (*Worker)(nil)

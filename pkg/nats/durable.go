package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/option"
	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	natsclient "github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type jetStreamClient interface {
	AccountInfo(...natsclient.JSOpt) (*natsclient.AccountInfo, error)
	AddStream(*natsclient.StreamConfig, ...natsclient.JSOpt) (*natsclient.StreamInfo, error)
	StreamInfo(string, ...natsclient.JSOpt) (*natsclient.StreamInfo, error)
	PublishMsg(*natsclient.Msg, ...natsclient.PubOpt) (*natsclient.PubAck, error)
	Subscribe(string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error)
	QueueSubscribe(string, string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error)
}

func (b *DurableBroker) EnsureStream(ctx context.Context, cfg StreamConfig) error {
	if b == nil || b.conn == nil || b.js == nil {
		return errors.New("ensure nats stream: broker is closed")
	}
	if !b.operations.begin() {
		return errors.New("ensure nats stream: broker is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return errors.New("ensure nats stream: context is required")
	}
	if err := validateStreamConfig(cfg); err != nil {
		return err
	}
	operationCtx, cancel := boundedContext(ctx, b.timeout)
	defer cancel()
	info, err := b.js.StreamInfo(cfg.Name, natsclient.Context(operationCtx))
	if errors.Is(err, natsclient.ErrStreamNotFound) {
		_, err = b.js.AddStream(&natsclient.StreamConfig{
			Name: cfg.Name, Subjects: append([]string(nil), cfg.Subjects...), Retention: natsclient.LimitsPolicy,
			Discard: natsclient.DiscardOld, Storage: natsclient.FileStorage, MaxAge: cfg.MaxAge,
			MaxBytes: cfg.MaxBytes, Duplicates: cfg.DuplicateWindow,
		}, natsclient.Context(operationCtx))
		if err != nil {
			return fmt.Errorf("create nats stream %q: %w", cfg.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect nats stream %q: %w", cfg.Name, err)
	}
	if info == nil || info.Config.Name != cfg.Name || !sameSubjects(info.Config.Subjects, cfg.Subjects) || info.Config.Retention != natsclient.LimitsPolicy || info.Config.Storage != natsclient.FileStorage || info.Config.MaxAge != cfg.MaxAge || info.Config.MaxBytes != cfg.MaxBytes || info.Config.Duplicates != cfg.DuplicateWindow {
		return fmt.Errorf("validate nats stream %q: deployed stream does not match the configured contract", cfg.Name)
	}
	return nil
}

func validateStreamConfig(cfg StreamConfig) error {
	if strings.TrimSpace(cfg.Name) == "" || len(cfg.Subjects) == 0 || cfg.MaxAge <= 0 || cfg.MaxBytes <= 0 || cfg.DuplicateWindow <= 0 {
		return errors.New("ensure nats stream: name, subjects, max age, max bytes, and duplicate window are required")
	}
	seen := make(map[string]struct{}, len(cfg.Subjects))
	for _, subject := range cfg.Subjects {
		if strings.TrimSpace(subject) == "" || strings.TrimSpace(subject) != subject {
			return errors.New("ensure nats stream: subjects must be non-empty and trimmed")
		}
		if _, exists := seen[subject]; exists {
			return errors.New("ensure nats stream: subjects must be unique")
		}
		seen[subject] = struct{}{}
	}
	return nil
}

func sameSubjects(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}

type jetStreamFactory func(time.Duration) (jetStreamClient, error)

type DurableBroker struct {
	conn          *natsclient.Conn
	closed        <-chan struct{}
	js            jetStreamClient
	newJetStream  jetStreamFactory
	logger        *slog.Logger
	timeout       time.Duration
	drainTimeout  time.Duration
	operations    operationGate
	shutdownState shutdown
	lifetime      brokerLifetime
}

func OpenDurable(ctx context.Context, opts option.NATSOptions, logger *slog.Logger) (*DurableBroker, error) {
	connection, err := connect(ctx, opts, logger)
	if err != nil {
		return nil, err
	}
	factory := func(timeout time.Duration) (jetStreamClient, error) {
		return connection.client.JetStream(natsclient.MaxWait(timeout))
	}
	js, err := factory(opts.RequestTimeout)
	if err != nil {
		connection.client.Close()
		return nil, fmt.Errorf("open nats JetStream: %w", err)
	}
	if err := queryJetStreamAccount(ctx, opts.RequestTimeout, js); err != nil {
		connection.client.Close()
		return nil, fmt.Errorf("verify nats JetStream readiness: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DurableBroker{
		conn:         connection.client,
		closed:       connection.closed,
		js:           js,
		newJetStream: factory,
		logger:       logger,
		timeout:      opts.RequestTimeout,
		drainTimeout: opts.DrainTimeout,
		lifetime:     newBrokerLifetime(),
	}, nil
}

func (b *DurableBroker) Publish(ctx context.Context, message Message, opts PublishOptions) error {
	if b == nil || b.conn == nil || b.js == nil {
		return errors.New("publish durable nats message: broker is closed")
	}
	if !b.operations.begin() {
		return errors.New("publish durable nats message: broker is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return errors.New("publish durable nats message: context is required")
	}
	if message.Subject == "" {
		return errors.New("publish durable nats message: subject is required")
	}
	ctx, span := startMessageSpan(ctx, "publish", message.ID, message.Subject, oteltrace.SpanKindProducer)
	defer span.End()
	msg := natsclient.NewMsg(message.Subject)
	msg.Data = append([]byte(nil), message.Body...)
	for key, value := range message.Headers {
		msg.Header.Set(key, value)
	}
	if message.ContentType != "" {
		msg.Header.Set("Content-Type", message.ContentType)
	}
	deduplicationID := opts.DeduplicationID
	if deduplicationID == "" {
		deduplicationID = message.ID
	}
	if deduplicationID != "" {
		msg.Header.Set(natsclient.MsgIdHdr, deduplicationID)
	}
	if message.ID != "" {
		msg.Header.Set("X-Message-ID", message.ID)
	}
	injectTrace(ctx, msg.Header)
	operationCtx, cancel := boundedContext(ctx, b.timeout)
	defer cancel()
	if _, err := b.js.PublishMsg(msg, natsclient.Context(operationCtx)); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish NATS message failed")
		return fmt.Errorf("publish durable nats message: %w", err)
	}
	span.SetAttributes(attribute.Int("messaging.batch.message_count", 1))
	return nil
}

func (b *DurableBroker) Subscribe(ctx context.Context, cfg ConsumerConfig, handler Handler) (*Subscription, error) {
	if b == nil || b.conn == nil || b.js == nil || b.newJetStream == nil {
		return nil, errors.New("subscribe durable nats consumer: broker is closed")
	}
	if !b.operations.begin() {
		return nil, errors.New("subscribe durable nats consumer: broker is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return nil, errors.New("subscribe durable nats consumer: context is required")
	}
	if err := validateConsumer(cfg, handler); err != nil {
		return nil, err
	}
	// A temporary nats.Context would become the subscription lifetime and
	// auto-unsubscribe when its setup deadline expires. Use MaxWait instead:
	// setup is bounded by caller deadline ∩ configuration without coupling the
	// resulting subscription to a short-lived operation context.
	requestTimeout, err := effectiveRequestTimeout(ctx, b.timeout)
	if err != nil {
		return nil, fmt.Errorf("subscribe durable nats consumer: %w", err)
	}
	js, err := b.newJetStream(requestTimeout)
	if err != nil {
		return nil, fmt.Errorf("configure durable nats consumer request timeout: %w", err)
	}
	opts := []natsclient.SubOpt{
		natsclient.ManualAck(),
		natsclient.AckExplicit(),
		natsclient.Durable(cfg.Durable),
		natsclient.BindStream(cfg.Stream),
	}
	if cfg.AckWait > 0 {
		opts = append(opts, natsclient.AckWait(cfg.AckWait))
	}
	if cfg.MaxDeliver > 0 {
		opts = append(opts, natsclient.MaxDeliver(cfg.MaxDeliver))
	}

	handlerCtx, cancelHandler := newSubscriptionLifetime(b.lifetime.context(), ctx)
	callback := func(msg *natsclient.Msg) {
		delivery := newDelivery(b.js, msg, cfg.DeadLetterSubject)
		deliveryCtx := extractTrace(handlerCtx, msg.Header)
		b.handleDelivery(deliveryCtx, cfg, handler, delivery)
	}
	var (
		sub          *natsclient.Subscription
		subscribeErr error
	)
	if cfg.Queue == "" {
		sub, subscribeErr = js.Subscribe(cfg.Subject, callback, opts...)
	} else {
		sub, subscribeErr = js.QueueSubscribe(cfg.Subject, cfg.Queue, callback, opts...)
	}
	if subscribeErr != nil {
		cancelHandler()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("subscribe durable nats consumer %q: %w", cfg.Durable, ctxErr)
		}
		return nil, fmt.Errorf("subscribe durable nats consumer %q: %w", cfg.Durable, subscribeErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		cancelHandler()
		var cleanupErr error
		if sub != nil {
			cleanupErr = sub.Unsubscribe()
		}
		return nil, errors.Join(
			fmt.Errorf("subscribe durable nats consumer %q: %w", cfg.Durable, ctxErr),
			cleanupErr,
		)
	}
	return newSubscription(sub, b.drainTimeout, cancelHandler), nil
}

func (b *DurableBroker) handleDelivery(ctx context.Context, cfg ConsumerConfig, handler Handler, delivery *Delivery) {
	// Retries stay attached to the first logical consume operation. Intermediate
	// deliveries are represented by bounded delivery metrics/logs instead of a
	// new span for every attempt.
	if delivery.attempt > 1 {
		ctx = coretrace.Suppress(ctx)
	}
	ctx, span := startMessageSpan(ctx, "consume", delivery.message.ID, delivery.message.Subject, oteltrace.SpanKindConsumer)
	defer span.End()
	span.SetAttributes(attribute.Int("messaging.delivery.attempt", delivery.attempt))
	defer func() {
		if recovered := recover(); recovered != nil {
			b.logger.ErrorContext(ctx, "nats consumer panicked",
				slog.String("stream", cfg.Stream),
				slog.String("consumer", cfg.Durable),
				slog.Any("panic", recovered),
			)
		}
		if !delivery.isSettled() {
			if err := delivery.Nack(ctx, 0); err != nil {
				b.logger.ErrorContext(ctx, "nats delivery auto-nack failed", slog.Any("error", err), slog.String("consumer", cfg.Durable))
			}
		}
	}()
	handler(ctx, delivery)
	if !delivery.isSettled() {
		span.SetStatus(codes.Error, "NATS delivery was not settled")
	}
}

func (b *DurableBroker) Ping(ctx context.Context) error {
	if b == nil || b.conn == nil || b.js == nil {
		return errors.New("ping durable nats connection: broker is closed")
	}
	if !b.operations.begin() {
		return errors.New("ping durable nats connection: broker is closing")
	}
	defer b.operations.end()
	if ctx == nil {
		return errors.New("ping durable nats connection: context is required")
	}
	if err := queryJetStreamAccount(ctx, b.timeout, b.js); err != nil {
		return fmt.Errorf("ping durable nats connection: %w", err)
	}
	return nil
}

func (b *DurableBroker) Close() error {
	if b == nil {
		return nil
	}
	b.operations.stop()
	return shutdownWithLifetime(&b.shutdownState, &b.lifetime, func() error {
		return drainConnection(b.conn, b.closed, "durable", b.drainTimeout)
	})
}

func queryJetStreamAccount(ctx context.Context, timeout time.Duration, js interface {
	AccountInfo(...natsclient.JSOpt) (*natsclient.AccountInfo, error)
}) error {
	if ctx == nil {
		return errors.New("query nats JetStream account: context is required")
	}
	if js == nil {
		return errors.New("query nats JetStream account: client is nil")
	}
	queryCtx, cancel := boundedContext(ctx, timeout)
	defer cancel()
	if _, err := js.AccountInfo(natsclient.Context(queryCtx)); err != nil {
		return fmt.Errorf("query nats JetStream account: %w", err)
	}
	return nil
}

func effectiveRequestTimeout(ctx context.Context, configured time.Duration) (time.Duration, error) {
	if ctx == nil {
		return 0, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		return min(configured, remaining), nil
	}
	return configured, nil
}

func validateConsumer(cfg ConsumerConfig, handler Handler) error {
	var result error
	if cfg.Stream == "" {
		result = errors.Join(result, errors.New("subscribe durable nats consumer: stream is required"))
	}
	if cfg.Durable == "" {
		result = errors.Join(result, errors.New("subscribe durable nats consumer: durable name is required"))
	}
	if cfg.Subject == "" {
		result = errors.Join(result, errors.New("subscribe durable nats consumer: subject is required"))
	}
	if cfg.AckWait < 0 {
		result = errors.Join(result, errors.New("subscribe durable nats consumer: ack wait must be non-negative"))
	}
	if cfg.MaxDeliver < 0 {
		result = errors.Join(result, errors.New("subscribe durable nats consumer: max deliver must be non-negative"))
	}
	if handler == nil {
		result = errors.Join(result, errors.New("subscribe durable nats consumer: handler is required"))
	}
	return result
}

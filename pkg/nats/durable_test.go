package nats

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsclient "github.com/nats-io/nats.go"
)

func TestQueryJetStreamAccountUsesBoundedContext(t *testing.T) {
	t.Parallel()
	const timeout = 250 * time.Millisecond
	called := false
	js := &fakeJetStream{
		accountInfo: func(opts ...natsclient.JSOpt) (*natsclient.AccountInfo, error) {
			called = true
			if len(opts) != 1 {
				t.Fatalf("AccountInfo() options = %d, want 1", len(opts))
			}
			contextOpt, ok := opts[0].(natsclient.ContextOpt)
			if !ok {
				t.Fatalf("AccountInfo() option type = %T, want nats.ContextOpt", opts[0])
			}
			deadline, ok := contextOpt.Deadline()
			if !ok {
				t.Fatal("AccountInfo() context has no deadline")
			}
			remaining := time.Until(deadline)
			if remaining <= 0 || remaining > timeout {
				t.Fatalf("AccountInfo() deadline remaining = %s, want (0, %s]", remaining, timeout)
			}
			return &natsclient.AccountInfo{}, nil
		},
	}
	if err := queryJetStreamAccount(context.Background(), timeout, js); err != nil {
		t.Fatalf("queryJetStreamAccount() error = %v", err)
	}
	if !called {
		t.Fatal("AccountInfo() was not called")
	}
}

func TestDurablePingUsesJetStreamAccountInfo(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("account unavailable")
	called := false
	js := &fakeJetStream{
		accountInfo: func(...natsclient.JSOpt) (*natsclient.AccountInfo, error) {
			called = true
			return nil, wantErr
		},
	}
	broker := &DurableBroker{
		conn:    &natsclient.Conn{},
		js:      js,
		timeout: time.Second,
	}
	err := broker.Ping(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Ping() error = %v, want %v", err, wantErr)
	}
	if !called {
		t.Fatal("Ping() did not call AccountInfo()")
	}
}

func TestEffectiveRequestTimeoutUsesCallerDeadlineAndConfiguration(t *testing.T) {
	t.Parallel()
	const configured = 5 * time.Second
	if got, err := effectiveRequestTimeout(context.Background(), configured); err != nil || got != configured {
		t.Fatalf("effectiveRequestTimeout(background) = (%s, %v), want (%s, nil)", got, err, configured)
	}

	callerCtx, cancelCaller := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancelCaller()
	got, err := effectiveRequestTimeout(callerCtx, configured)
	if err != nil {
		t.Fatalf("effectiveRequestTimeout(deadline) error = %v", err)
	}
	if got <= 0 || got > 250*time.Millisecond {
		t.Fatalf("effectiveRequestTimeout(deadline) = %s, want (0, 250ms]", got)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := effectiveRequestTimeout(canceledCtx, configured); !errors.Is(err, context.Canceled) {
		t.Fatalf("effectiveRequestTimeout(canceled) error = %v, want context.Canceled", err)
	}
}

func TestDurableSubscribeRetriesExclusiveBindUntilSuccess(t *testing.T) {
	restoreExclusiveBindWait(t)
	waitExclusiveBind = func(context.Context, time.Duration) error { return nil }

	boundErr := errors.New("consumer is already bound to a subscription")
	attempts := 0
	js := &fakeJetStream{
		subscribe: func(string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error) {
			attempts++
			if attempts < 3 {
				return nil, boundErr
			}
			return nil, nil
		},
	}
	broker := newTestDurableBroker(js)
	sub, err := broker.Subscribe(context.Background(), ConsumerConfig{
		Stream:  "KNOWLEDGE_CORE_CONFIG",
		Durable: "identity-email-config-v1",
		Subject: "platform.config.changed.v1",
	}, func(context.Context, *Delivery) {})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	if sub == nil {
		t.Fatal("Subscribe() subscription = nil")
	}
	if attempts != 3 {
		t.Fatalf("Subscribe() attempts = %d, want 3", attempts)
	}
}

func TestDurableSubscribeDoesNotRetryOtherSubscribeErrors(t *testing.T) {
	restoreExclusiveBindWait(t)
	waitExclusiveBind = func(context.Context, time.Duration) error {
		t.Fatal("waitExclusiveBind() called for a non-bind error")
		return nil
	}

	wantErr := errors.New("stream not found")
	attempts := 0
	js := &fakeJetStream{
		subscribe: func(string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error) {
			attempts++
			return nil, wantErr
		},
	}
	_, err := newTestDurableBroker(js).Subscribe(context.Background(), ConsumerConfig{
		Stream:  "events",
		Durable: "identity",
		Subject: "identity.created",
	}, func(context.Context, *Delivery) {})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Subscribe() error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("Subscribe() attempts = %d, want 1", attempts)
	}
}

func TestDurableSubscribeDoesNotRetryQueueConsumerBindErrors(t *testing.T) {
	restoreExclusiveBindWait(t)
	waitExclusiveBind = func(context.Context, time.Duration) error {
		t.Fatal("waitExclusiveBind() called for a queue subscription")
		return nil
	}

	boundErr := errors.New("consumer is already bound to a subscription")
	attempts := 0
	js := &fakeJetStream{
		queueSubscribe: func(string, string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error) {
			attempts++
			return nil, boundErr
		},
	}
	_, err := newTestDurableBroker(js).Subscribe(context.Background(), ConsumerConfig{
		Stream:  "events",
		Durable: "identity",
		Queue:   "identity-workers",
		Subject: "identity.created",
	}, func(context.Context, *Delivery) {})
	if !errors.Is(err, boundErr) {
		t.Fatalf("Subscribe() error = %v, want %v", err, boundErr)
	}
	if attempts != 1 {
		t.Fatalf("QueueSubscribe() attempts = %d, want 1", attempts)
	}
}

func TestDurableSubscribeStopsExclusiveBindRetryWhenContextCanceled(t *testing.T) {
	restoreExclusiveBindWait(t)

	ctx, cancel := context.WithCancel(context.Background())
	waitExclusiveBind = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	js := &fakeJetStream{
		subscribe: func(string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error) {
			return nil, errors.New("consumer is already bound to a subscription")
		},
	}
	_, err := newTestDurableBroker(js).Subscribe(ctx, ConsumerConfig{
		Stream:  "events",
		Durable: "identity",
		Subject: "identity.created",
	}, func(context.Context, *Delivery) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Subscribe() error = %v, want context.Canceled", err)
	}
}

func newTestDurableBroker(js *fakeJetStream) *DurableBroker {
	return &DurableBroker{
		conn: &natsclient.Conn{},
		js:   js,
		newJetStream: func(time.Duration) (jetStreamClient, error) {
			return js, nil
		},
		logger:       slog.Default(),
		timeout:      time.Second,
		drainTimeout: time.Second,
	}
}

func restoreExclusiveBindWait(t *testing.T) {
	t.Helper()
	original := waitExclusiveBind
	t.Cleanup(func() { waitExclusiveBind = original })
}

func TestDurableSubscribePassesEffectiveMaxWait(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("subscribe failed")
	js := &fakeJetStream{subscribeErr: wantErr}
	var receivedTimeout time.Duration
	broker := &DurableBroker{
		conn: &natsclient.Conn{},
		js:   js,
		newJetStream: func(timeout time.Duration) (jetStreamClient, error) {
			receivedTimeout = timeout
			return js, nil
		},
		logger:       slog.Default(),
		timeout:      5 * time.Second,
		drainTimeout: time.Second,
	}
	callerCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := broker.Subscribe(callerCtx, ConsumerConfig{
		Stream:  "events",
		Durable: "identity",
		Subject: "identity.created",
	}, func(context.Context, *Delivery) {})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Subscribe() error = %v, want %v", err, wantErr)
	}
	if receivedTimeout <= 0 || receivedTimeout > 250*time.Millisecond {
		t.Fatalf("JetStream MaxWait = %s, want (0, 250ms]", receivedTimeout)
	}
}

func TestOperationsRejectNewWorkAfterClosingBegins(t *testing.T) {
	t.Parallel()
	js := &fakeJetStream{}
	durable := &DurableBroker{
		conn: &natsclient.Conn{},
		js:   js,
		newJetStream: func(time.Duration) (jetStreamClient, error) {
			return js, nil
		},
	}
	durable.operations.stop()
	if err := durable.Publish(context.Background(), Message{Subject: "events"}, PublishOptions{}); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("DurableBroker.Publish() error = %v, want closing", err)
	}
	if _, err := durable.Subscribe(context.Background(), ConsumerConfig{}, func(context.Context, *Delivery) {}); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("DurableBroker.Subscribe() error = %v, want closing", err)
	}
	if err := durable.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("DurableBroker.Ping() error = %v, want closing", err)
	}

	realtime := &RealtimeBus{conn: &natsclient.Conn{}}
	realtime.operations.stop()
	if err := realtime.Publish(context.Background(), "events", nil); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("RealtimeBus.Publish() error = %v, want closing", err)
	}
	if _, err := realtime.Subscribe(context.Background(), "events", func(context.Context, string, []byte) {}); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("RealtimeBus.Subscribe() error = %v, want closing", err)
	}
	if err := realtime.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("RealtimeBus.Ping() error = %v, want closing", err)
	}
}

func TestEnsureStreamCreatesMissingStream(t *testing.T) {
	t.Parallel()
	var created *natsclient.StreamConfig
	js := &fakeJetStream{
		streamInfoErr: natsclient.ErrStreamNotFound,
		addStream: func(config *natsclient.StreamConfig) (*natsclient.StreamInfo, error) {
			created = config
			return &natsclient.StreamInfo{Config: *config}, nil
		},
	}
	broker := &DurableBroker{conn: &natsclient.Conn{}, js: js, timeout: time.Second}
	config := StreamConfig{Name: "CONFIG", Subjects: []string{"platform.config.changed.v1"}, MaxAge: time.Hour, MaxBytes: 1024, DuplicateWindow: time.Minute}
	if err := broker.EnsureStream(context.Background(), config); err != nil {
		t.Fatalf("EnsureStream() error = %v", err)
	}
	if created == nil || created.Name != config.Name || created.Storage != natsclient.FileStorage || created.Duplicates != config.DuplicateWindow {
		t.Fatalf("created stream = %#v", created)
	}
}

func TestEnsureStreamRejectsContractDrift(t *testing.T) {
	t.Parallel()
	js := &fakeJetStream{streamInfo: &natsclient.StreamInfo{Config: natsclient.StreamConfig{
		Name: "CONFIG", Subjects: []string{"platform.config.changed.v1"}, Retention: natsclient.LimitsPolicy,
		Storage: natsclient.MemoryStorage, MaxAge: time.Hour, MaxBytes: 1024, Duplicates: time.Minute,
	}}}
	broker := &DurableBroker{conn: &natsclient.Conn{}, js: js, timeout: time.Second}
	err := broker.EnsureStream(context.Background(), StreamConfig{Name: "CONFIG", Subjects: []string{"platform.config.changed.v1"}, MaxAge: time.Hour, MaxBytes: 1024, DuplicateWindow: time.Minute})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("EnsureStream() error = %v, want contract mismatch", err)
	}
}

type fakeJetStream struct {
	accountInfo    func(...natsclient.JSOpt) (*natsclient.AccountInfo, error)
	addStream      func(*natsclient.StreamConfig) (*natsclient.StreamInfo, error)
	streamInfo     *natsclient.StreamInfo
	streamInfoErr  error
	subscribeErr   error
	subscribe      func(string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error)
	queueSubscribe func(string, string, natsclient.MsgHandler, ...natsclient.SubOpt) (*natsclient.Subscription, error)
}

func (f *fakeJetStream) AccountInfo(opts ...natsclient.JSOpt) (*natsclient.AccountInfo, error) {
	if f.accountInfo == nil {
		return &natsclient.AccountInfo{}, nil
	}
	return f.accountInfo(opts...)
}

func (f *fakeJetStream) AddStream(config *natsclient.StreamConfig, _ ...natsclient.JSOpt) (*natsclient.StreamInfo, error) {
	if f.addStream != nil {
		return f.addStream(config)
	}
	return &natsclient.StreamInfo{}, nil
}

func (f *fakeJetStream) StreamInfo(string, ...natsclient.JSOpt) (*natsclient.StreamInfo, error) {
	if f.streamInfo != nil || f.streamInfoErr != nil {
		return f.streamInfo, f.streamInfoErr
	}
	return nil, natsclient.ErrStreamNotFound
}

func (f *fakeJetStream) PublishMsg(*natsclient.Msg, ...natsclient.PubOpt) (*natsclient.PubAck, error) {
	return &natsclient.PubAck{}, nil
}

func (f *fakeJetStream) Subscribe(
	subject string,
	handler natsclient.MsgHandler,
	opts ...natsclient.SubOpt,
) (*natsclient.Subscription, error) {
	if f.subscribe != nil {
		return f.subscribe(subject, handler, opts...)
	}
	return nil, f.subscribeErr
}

func (f *fakeJetStream) QueueSubscribe(
	subject string,
	queue string,
	handler natsclient.MsgHandler,
	opts ...natsclient.SubOpt,
) (*natsclient.Subscription, error) {
	if f.queueSubscribe != nil {
		return f.queueSubscribe(subject, queue, handler, opts...)
	}
	return nil, f.subscribeErr
}

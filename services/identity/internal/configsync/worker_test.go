package configsync

import (
	"context"
	"log/slog"
	"testing"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	"github.com/cloudwego/kitex/client/callopt"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type platformStub struct {
	state       *platformv1.ConsumerConfigurationState
	stateErr    error
	configCalls int
}

func (s *platformStub) GetConsumerState(context.Context, *platformv1.GetConsumerStateRequest, ...callopt.Option) (*platformv1.ConsumerConfigurationState, error) {
	return s.state, s.stateErr
}

func (s *platformStub) GetConsumerConfiguration(context.Context, *platformv1.GetConsumerConfigurationRequest, ...callopt.Option) (*platformv1.Configuration, error) {
	s.configCalls++
	return &platformv1.Configuration{Namespace: "email"}, nil
}

func (s *platformStub) ReportConfigurationApply(context.Context, *platformv1.ReportConfigurationApplyRequest, ...callopt.Option) (*commonv1.EmptyResponse, error) {
	return &commonv1.EmptyResponse{}, nil
}

func TestSMTPOptionsIncludesInternalSecretValues(t *testing.T) {
	configuration := &platformv1.Configuration{
		Namespace: "email",
		Values: []*platformv1.ConfigValue{
			{Key: "host", Value: "mail.example.test"},
			{Key: "port", Value: "587"},
			{Key: "username", Value: "mailer@example.test"},
			{Key: "password", Value: "secret"},
			{Key: "from", Value: "Knowledge Core <mailer@example.test>"},
			{Key: "frontend_base_url", Value: "https://example.test"},
		},
	}
	options, err := smtpOptions(configuration)
	if err != nil {
		t.Fatalf("smtpOptions() error = %v", err)
	}
	if options.Host != "mail.example.test" || options.Port != 587 || options.Password != "secret" {
		t.Fatalf("smtpOptions() = %#v", options)
	}
}

func TestSMTPOptionsRejectsInvalidRevisionPayload(t *testing.T) {
	if _, err := smtpOptions(&platformv1.Configuration{Namespace: "email", Values: []*platformv1.ConfigValue{{Key: "port", Value: "not-a-port"}}}); err == nil {
		t.Fatal("smtpOptions() accepted an invalid port")
	}
	if _, err := smtpOptions(&platformv1.Configuration{Namespace: "site"}); err == nil {
		t.Fatal("smtpOptions() accepted a non-email namespace")
	}
}

func TestReconcileNoopsWhenDesiredRevisionIsZero(t *testing.T) {
	stub := &platformStub{state: &platformv1.ConsumerConfigurationState{DesiredRevision: 0, AppliedRevision: 0}}
	worker := &Worker{platform: stub, serviceToken: "token", logger: slog.Default()}
	if err := worker.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() error = %v", err)
	}
	if stub.configCalls != 0 {
		t.Fatalf("GetConsumerConfiguration() calls = %d, want 0", stub.configCalls)
	}
}

func TestReconcileStartsParentSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	stub := &platformStub{state: &platformv1.ConsumerConfigurationState{DesiredRevision: 0}}
	worker := &Worker{platform: stub, serviceToken: "token", logger: slog.Default()}
	if err := worker.reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile() error = %v", err)
	}
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "identity.config.reconcile" {
		t.Fatalf("spans = %v, want identity.config.reconcile", spanNames(spans))
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	return names
}

func TestRetryDelayIsBounded(t *testing.T) {
	if got := retryDelay(1); got != 10*time.Second {
		t.Fatalf("retryDelay(1) = %s", got)
	}
	if got := retryDelay(8); got != 80*time.Second {
		t.Fatalf("retryDelay(8) = %s", got)
	}
}

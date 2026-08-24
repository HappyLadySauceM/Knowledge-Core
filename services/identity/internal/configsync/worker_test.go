package configsync

import (
	"testing"
	"time"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
)

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

func TestRetryDelayIsBounded(t *testing.T) {
	if got := retryDelay(1); got != 10*time.Second {
		t.Fatalf("retryDelay(1) = %s", got)
	}
	if got := retryDelay(8); got != 80*time.Second {
		t.Fatalf("retryDelay(8) = %s", got)
	}
}

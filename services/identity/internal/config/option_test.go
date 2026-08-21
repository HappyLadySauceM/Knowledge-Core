package config

import "testing"

func TestSMTPOptionsRequireAuthenticatedSubmission(t *testing.T) {
	options := SMTPOptions{Host: "mail.example.test", Port: 587, From: "Knowledge Core <core@example.test>", FrontendBaseURL: "https://example.test"}
	if err := options.Validate(); err == nil {
		t.Fatal("expected SMTP credentials to be required")
	}
	options.Username = "core@example.test"
	options.Password = "test-password"
	if err := options.Validate(); err != nil {
		t.Fatalf("valid SMTP options rejected: %v", err)
	}
}

func TestSMTPOptionsAllowDevelopmentDisabledMode(t *testing.T) {
	if err := (SMTPOptions{}).Validate(); err != nil {
		t.Fatalf("disabled SMTP should be valid: %v", err)
	}
}

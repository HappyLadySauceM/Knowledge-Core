package domain

import (
	"errors"
	"testing"
)

func TestValidateEmailPreservesOmittedSecret(t *testing.T) {
	t.Parallel()

	public, secrets, err := Validate("email", map[string]string{
		"enabled": "true", "host": "smtp.example.com", "port": "587",
		"username": "mailer@example.com", "from": "Knowledge Core <mailer@example.com>",
		"frontend_base_url": "https://example.com",
	}, map[string]string{"password": "existing-secret"})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if public["host"] != "smtp.example.com" {
		t.Fatalf("host = %q", public["host"])
	}
	if secrets["password"] != "existing-secret" {
		t.Fatalf("password = %q, want preserved secret", secrets["password"])
	}
}

func TestValidateRejectsUnknownAndInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
		values    map[string]string
	}{
		{name: "unknown namespace", namespace: "unknown", values: map[string]string{"enabled": "false"}},
		{name: "unknown key", namespace: "site", values: map[string]string{"unexpected": "value"}},
		{name: "invalid focal point", namespace: "site", values: map[string]string{"hero_focal_x": "101"}},
		{name: "invalid attachment", namespace: "site", values: map[string]string{"hero_attachment_id": "not-a-uuid"}},
		{name: "insecure AI endpoint", namespace: "ai", values: map[string]string{
			"enabled": "true", "base_url": "http://ai.example.com", "model": "model", "api_key": "secret",
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := Validate(test.namespace, test.values, nil); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestValidateSecretCanBeRotatedAndCleared(t *testing.T) {
	t.Parallel()

	_, rotated, err := Validate("ai", map[string]string{"api_key": "next-secret"}, map[string]string{"api_key": "old-secret"})
	if err != nil {
		t.Fatalf("Validate(rotation) error = %v", err)
	}
	if rotated["api_key"] != "next-secret" {
		t.Fatalf("rotated api_key = %q", rotated["api_key"])
	}
	_, cleared, err := Validate("ai", map[string]string{"api_key": ""}, rotated)
	if err != nil {
		t.Fatalf("Validate(clear) error = %v", err)
	}
	if _, ok := cleared["api_key"]; ok {
		t.Fatal("cleared api_key is still present")
	}
}

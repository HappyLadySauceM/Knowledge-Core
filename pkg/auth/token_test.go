package auth

import (
	"testing"
	"time"
)

func TestIssuerAndVerifierRoundTrip(t *testing.T) {
	keys, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	if err := ValidateKeyPair(keys.PrivateKey, keys.PublicKey); err != nil {
		t.Fatalf("ValidateKeyPair() error = %v", err)
	}
	issuer, err := NewIssuer(keys.PrivateKey, 15*time.Minute)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	verifier, err := NewVerifier(keys.PublicKey)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	issuer.now = func() time.Time { return now }
	verifier.now = func() time.Time { return now }

	issued, err := issuer.Issue(Principal{UserID: 42, Role: "user", TokenVersion: 3})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !issued.ExpiresAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("ExpiresAt = %v", issued.ExpiresAt)
	}
	principal, err := verifier.Verify(issued.Value)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal != (Principal{UserID: 42, Role: "user", TokenVersion: 3, ExpiresAt: issued.ExpiresAt}) {
		t.Fatalf("principal = %#v", principal)
	}

	verifier.now = func() time.Time { return now.Add(16 * time.Minute) }
	if _, err := verifier.Verify(issued.Value); err == nil {
		t.Fatal("Verify() accepted an expired token")
	}
}

func TestVerifierRejectsWrongKeyAndOversizedToken(t *testing.T) {
	issuerKeys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	verifierKeys, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeyPair(issuerKeys.PrivateKey, verifierKeys.PublicKey); err == nil {
		t.Fatal("ValidateKeyPair() accepted mismatched keys")
	}
	issuer, _ := NewIssuer(issuerKeys.PrivateKey, time.Minute)
	verifier, _ := NewVerifier(verifierKeys.PublicKey)
	issued, err := issuer.Issue(Principal{UserID: 1, Role: "admin", TokenVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(issued.Value); err == nil {
		t.Fatal("Verify() accepted a token signed by another key")
	}
	if _, err := verifier.Verify(string(make([]byte, MaxTokenLength+1))); err == nil {
		t.Fatal("Verify() accepted an oversized token")
	}
}

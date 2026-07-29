package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestIssuerAndVerifierRoundTrip(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	issuer, err := NewIssuer(base64.StdEncoding.EncodeToString(privateKey), 15*time.Minute)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	now := time.Date(2026, 7, 27, 4, 0, 0, 0, time.UTC)
	issuer.now = func() time.Time { return now }
	issuer.random = strings.NewReader("0123456789abcdef")
	issued, err := issuer.Issue(Principal{UserID: 42, Role: "admin", TokenVersion: 3})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	verifier, err := NewVerifier(base64.StdEncoding.EncodeToString(publicKey))
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	verifier.now = func() time.Time { return now }
	principal, err := verifier.Verify(issued.Value)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if principal.UserID != 42 || principal.Role != "admin" || principal.TokenVersion != 3 {
		t.Fatalf("Verify() principal = %#v", principal)
	}
	if !issued.ExpiresAt.Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("Issue() expires at = %v", issued.ExpiresAt)
	}
}

func TestGenerateKeyPair(t *testing.T) {
	keyPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	issuer, err := NewIssuer(keyPair.PrivateKey, time.Minute)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	verifier, err := NewVerifier(keyPair.PublicKey)
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	issued, err := issuer.Issue(Principal{UserID: 1, Role: "user", TokenVersion: 1})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := verifier.Verify(issued.Value); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifierRejectsWrongKey(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	publicKey, _, _ := ed25519.GenerateKey(rand.Reader)
	issuer, _ := NewIssuer(base64.StdEncoding.EncodeToString(privateKey), time.Minute)
	issued, _ := issuer.Issue(Principal{UserID: 1, Role: "user", TokenVersion: 1})
	verifier, _ := NewVerifier(base64.StdEncoding.EncodeToString(publicKey))
	if _, err := verifier.Verify(issued.Value); err == nil {
		t.Fatal("Verify() accepted a token signed by another key")
	}
}

func TestVerifierRejectsExpiredToken(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	issuer, _ := NewIssuer(base64.StdEncoding.EncodeToString(privateKey), time.Minute)
	now := time.Date(2026, 7, 27, 4, 0, 0, 0, time.UTC)
	issuer.now = func() time.Time { return now }
	issued, _ := issuer.Issue(Principal{UserID: 1, Role: "user", TokenVersion: 1})
	verifier, _ := NewVerifier(base64.StdEncoding.EncodeToString(publicKey))
	verifier.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := verifier.Verify(issued.Value); err == nil {
		t.Fatal("Verify() accepted an expired token")
	}
}

func TestKeyConfigurationIsRequired(t *testing.T) {
	if _, err := NewIssuer("", time.Minute); err == nil {
		t.Fatal("NewIssuer() accepted an empty private key")
	}
	if _, err := NewVerifier(""); err == nil {
		t.Fatal("NewVerifier() accepted an empty public key")
	}
}

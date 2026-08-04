package configcenter

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestEnvelopeRoundTripIsBoundToNacosCoordinates(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x42}, keySize)
	binding := Binding{Namespace: "test", Group: "KNOWLEDGE_CORE", DataID: "gateway.dynamic.yaml"}
	plaintext := []byte("api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 1\nlog:\n  level: info\n")
	envelope, err := Encrypt(plaintext, key, "config-2026-08", binding)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	decoded, err := Decrypt(envelope, key, "config-2026-08", binding)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("plaintext mismatch: got %q", decoded)
	}

	other := binding
	other.DataID = "identity.dynamic.yaml"
	if _, err := Decrypt(envelope, key, "config-2026-08", other); err == nil {
		t.Fatal("decrypting with a different data ID must fail")
	}
}

func TestEnvelopeRejectsTamperingAndUnknownFields(t *testing.T) {
	t.Parallel()
	key := bytes.Repeat([]byte{0x23}, keySize)
	binding := Binding{Namespace: "prod", Group: "KNOWLEDGE_CORE", DataID: "knowledge.dynamic.yaml"}
	envelope, err := Encrypt([]byte("valid"), key, "key-1", binding)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	tampered := append([]byte(nil), envelope...)
	tampered[len(tampered)-4] ^= 1
	if _, err := Decrypt(tampered, key, "key-1", binding); err == nil {
		t.Fatal("tampered envelope must fail")
	}
	unknown := bytes.Replace(envelope, []byte(`"schema"`), []byte(`"unknown":true,"schema"`), 1)
	if _, err := Decrypt(unknown, key, "key-1", binding); err == nil {
		t.Fatal("unknown envelope field must fail")
	}
}

func TestParseKeyRequiresCanonicalBase64AES256Key(t *testing.T) {
	t.Parallel()
	encoded := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, keySize))
	key, err := ParseKey(encoded)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	if len(key) != keySize {
		t.Fatalf("key length: got %d", len(key))
	}
	for _, value := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := ParseKey(value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
}

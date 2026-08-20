package security

import (
	"bytes"
	"testing"
)

func TestRefreshTokenRoundTripAndDigest(t *testing.T) {
	token, err := NewRefreshToken(bytes.NewReader(bytes.Repeat([]byte{7}, 48)))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := token.Encode()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRefreshToken(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != token {
		t.Fatalf("parsed = %#v, want %#v", parsed, token)
	}
	if !bytes.Equal(DigestRefreshSecret(token.Secret, []byte("pepper")), DigestRefreshSecret(token.Secret, []byte("pepper"))) {
		t.Fatal("digest is not deterministic")
	}
	if bytes.Equal(DigestRefreshSecret(token.Secret, []byte("pepper")), DigestRefreshSecret(token.Secret, []byte("other"))) {
		t.Fatal("digest ignored pepper")
	}
}

func TestParseRefreshTokenRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "kc1.bad", "kc2.a.b", "kc1.a.b", "kc1.YWFh.Yg"} {
		if _, err := ParseRefreshToken(value); err == nil {
			t.Errorf("ParseRefreshToken(%q) accepted malformed input", value)
		}
	}
}

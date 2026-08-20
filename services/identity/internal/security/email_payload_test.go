package security

import "testing"

func TestEmailPayloadRoundTripAndWrongKey(t *testing.T) {
	payload, err := SealEmailToken("ka1.secret", "01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "ka1.secret" {
		t.Fatal("email token was persisted in plaintext")
	}
	value, err := OpenEmailToken(payload, "01234567890123456789012345678901")
	if err != nil || value != "ka1.secret" {
		t.Fatalf("round trip = %q, %v", value, err)
	}
	if _, err := OpenEmailToken(payload, "different-key"); err == nil {
		t.Fatal("wrong key unexpectedly decrypted email token")
	}
}

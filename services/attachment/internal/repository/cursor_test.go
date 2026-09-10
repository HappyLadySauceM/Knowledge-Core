package repository

import (
	"testing"
	"time"
)

func TestAttachmentCursorRoundTrip(t *testing.T) {
	want := Cursor{Time: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), ID: "0198a3c0-0000-7000-8000-000000000001"}
	encoded, err := EncodeCursor(want)
	if err != nil {
		t.Fatalf("EncodeCursor() error = %v", err)
	}
	got, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor() error = %v", err)
	}
	if got.Version != 1 || !got.Time.Equal(want.Time) || got.ID != want.ID {
		t.Fatalf("DecodeCursor() = %#v, want %#v", got, want)
	}
}

func TestAttachmentCursorRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"not-base64", "e30"} {
		if _, err := DecodeCursor(value); err == nil {
			t.Fatalf("DecodeCursor(%q) unexpectedly succeeded", value)
		}
	}
}

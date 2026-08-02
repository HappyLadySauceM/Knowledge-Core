package domain

import (
	"strings"
	"testing"
)

func TestNewIDProducesUUIDv7(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if err := ValidateID("id", id); err != nil {
		t.Fatalf("ValidateID() error = %v", err)
	}
	if err := ValidateID("id", "550e8400-e29b-41d4-a716-446655440000"); err == nil {
		t.Fatal("ValidateID() accepted a UUIDv4")
	}
}

func TestNormalizeSlugRejectsReservedAndMalformedValues(t *testing.T) {
	for _, value := range []string{"api", "Health", "two--hyphens", "a", "non_ascii"} {
		if _, err := NormalizeSlug(value); err == nil {
			t.Fatalf("NormalizeSlug(%q) accepted an invalid slug", value)
		}
	}
	if got, err := NormalizeSlug("  Useful-Document  "); err != nil || got != "useful-document" {
		t.Fatalf("NormalizeSlug() = %q, %v", got, err)
	}
}

func TestValidateAttachmentEnforcesMediaLimitsAndChecksum(t *testing.T) {
	checksum := strings.Repeat("a", 64)
	tests := []struct {
		name      string
		filename  string
		mediaType string
		size      int64
		checksum  string
		wantError bool
	}{
		{name: "image limit", filename: "photo.png", mediaType: "image/png", size: MaxImageBytes},
		{name: "image too large", filename: "photo.png", mediaType: "image/png", size: MaxImageBytes + 1, wantError: true},
		{name: "file limit", filename: "archive.zip", mediaType: "application/zip", size: MaxFileBytes},
		{name: "file too large", filename: "archive.zip", mediaType: "application/zip", size: MaxFileBytes + 1, wantError: true},
		{name: "path filename", filename: "../secret.txt", mediaType: "text/plain", size: 1, wantError: true},
		{name: "bad checksum", filename: "file.txt", mediaType: "text/plain", size: 1, checksum: "ABC", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := test.checksum
			if value == "" {
				value = checksum
			}
			err := ValidateAttachment(test.filename, test.mediaType, test.size, value)
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateAttachment() error = %v, wantError = %v", err, test.wantError)
			}
		})
	}
}

func TestRichTextValidationRejectsUnsafeLinksAndInvalidTrees(t *testing.T) {
	unsafe := "javascript:alert(1)"
	text := "click"
	document := RichTextDocument{Type: "doc", Content: []*RichTextNode{{
		Type: "paragraph", Content: []*RichTextNode{{
			Type: "text", Text: &text,
			Marks: []RichTextMark{{Type: "link", Attrs: &RichTextAttrs{Href: &unsafe}}},
		}},
	}}}
	if err := document.Validate(); err == nil {
		t.Fatal("Validate() accepted a javascript link")
	}
	document.Content[0].Content[0].Marks = nil
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() rejected a valid document: %v", err)
	}
}

package domain

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewIDIsUUIDv7(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("NewID() returned invalid UUID: %v", err)
	}
	if got := parsed.Version(); got != 7 {
		t.Fatalf("NewID() version = %v, want 7", got)
	}
}

func TestValidateCategoriesAndLimits(t *testing.T) {
	tests := []struct{ name, media, want string }{
		{"image", "image/webp", CategoryImage}, {"archive", "application/x-7z-compressed", CategoryArchive}, {"document", "application/pdf", CategoryDocument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Validate("safe."+tt.name, tt.media, 1)
			if err != nil || got != tt.want {
				t.Fatalf("Validate() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, media := range []string{"application/x-msdownload", "text/html"} {
		if _, err := Validate("file.bin", media, 1); err == nil {
			t.Fatalf("Validate(%q) accepted blocked media", media)
		}
	}
	if category, err := Validate("file.bin", "application/octet-stream", 1); err != nil || category != CategoryFile {
		t.Fatalf("Validate(application/octet-stream) = %q, %v; want file", category, err)
	}
	if _, err := Validate("file.bin", "application/pdf", 1<<30+1); err == nil {
		t.Fatal("Validate accepted a file larger than 1 GiB")
	}
}

func TestValidateRejectsPathAndOversizedFilename(t *testing.T) {
	if _, err := Validate("../escape.pdf", "application/pdf", 1); err == nil {
		t.Fatal("path traversal filename accepted")
	}
	if _, err := Validate("a", "application/pdf", 0); err == nil {
		t.Fatal("empty attachment accepted")
	}
}

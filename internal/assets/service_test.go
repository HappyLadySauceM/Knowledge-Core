package assets

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDetectImageType(t *testing.T) {
	tests := []struct {
		name          string
		header        []byte
		wantType      string
		wantExtension string
		wantOK        bool
	}{
		{name: "png", header: []byte("\x89PNG\r\n\x1a\n"), wantType: "image/png", wantExtension: ".png", wantOK: true},
		{name: "avif", header: []byte("....ftypavif"), wantType: "image/avif", wantExtension: ".avif", wantOK: true},
		{name: "text", header: []byte("plain text"), wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotExtension, gotOK := detectImageType(tt.header)
			if gotType != tt.wantType || gotExtension != tt.wantExtension || gotOK != tt.wantOK {
				t.Fatalf("detectImageType() = %q, %q, %v; want %q, %q, %v", gotType, gotExtension, gotOK, tt.wantType, tt.wantExtension, tt.wantOK)
			}
		})
	}
}

func TestSafeFilename(t *testing.T) {
	if got := safeFilename(`..\..\cover.jpg`); got != "cover.jpg" {
		t.Fatalf("safeFilename() = %q, want cover.jpg", got)
	}
	if got := safeFilename("\n\t"); got != "upload" {
		t.Fatalf("safeFilename(control chars) = %q, want upload", got)
	}
	got := safeFilename(strings.Repeat("文", 100))
	if len(got) > 255 || !utf8.ValidString(got) {
		t.Fatalf("safeFilename(utf8) = %q (%d bytes), want valid UTF-8 at most 255 bytes", got, len(got))
	}
}

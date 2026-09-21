package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

func TestCanonicalPublicationHashUsesStableEnvelopeOrdering(t *testing.T) {
	contentHash := sha256.Sum256([]byte(`[{"content":[{"text":"draft","type":"paragraph"}],"type":"doc"},"draft"]`))
	got := canonicalPublicationHash("Title", "Summary", "title", "zh-CN", []string{"one", "two"}, hex.EncodeToString(contentHash[:]), "📚", "attachment", floatPointer(50), floatPointer(25))

	wantPayload, err := json.Marshal(map[string]any{
		"title":               "Title",
		"summary":             "Summary",
		"slug":                "title",
		"language":            "zh-CN",
		"tags":                []string{"one", "two"},
		"content_hash":        hex.EncodeToString(contentHash[:]),
		"icon":                "📚",
		"cover_attachment_id": "attachment",
		"cover_focal_x":       floatPointer(50),
		"cover_focal_y":       floatPointer(25),
	})
	if err != nil {
		t.Fatalf("marshal expected hash payload: %v", err)
	}
	wantDigest := sha256.Sum256(wantPayload)
	if got != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("canonicalPublicationHash() = %s, want %s", got, hex.EncodeToString(wantDigest[:]))
	}
}

func floatPointer(value float64) *float64 { return &value }

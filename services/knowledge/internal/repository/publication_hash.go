package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
)

// publicationHashForSnapshot is the compatibility path for snapshots created
// before publication_hash was introduced. It deliberately mirrors the
// Gateway/browser canonical envelope so opening an unchanged legacy document
// does not incorrectly offer an update.
func publicationHashForSnapshot(snapshot *model.DocumentPublication) (string, error) {
	if snapshot == nil {
		return "", fmt.Errorf("publication snapshot is nil")
	}
	var content any
	if err := jsoncodec.Unmarshal(snapshot.Content, &content); err != nil {
		return "", fmt.Errorf("decode publication content: %w", err)
	}
	contentPayload, err := jsoncodec.Marshal([]any{content, snapshot.PlainText})
	if err != nil {
		return "", fmt.Errorf("encode publication content hash: %w", err)
	}
	contentDigest := sha256.Sum256(contentPayload)
	contentHash := hex.EncodeToString(contentDigest[:])
	cover := ""
	if snapshot.CoverAttachmentID != nil {
		cover = *snapshot.CoverAttachmentID
	}
	payload, err := jsoncodec.Marshal(map[string]any{
		"title":               snapshot.Title,
		"summary":             snapshot.Summary,
		"slug":                snapshot.Slug,
		"language":            snapshot.Language,
		"tags":                publicationTags(snapshot.Tags),
		"content_hash":        contentHash,
		"icon":                snapshot.Icon,
		"cover_attachment_id": cover,
		"cover_focal_x":       snapshot.CoverFocalX,
		"cover_focal_y":       snapshot.CoverFocalY,
	})
	if err != nil {
		return "", fmt.Errorf("encode publication hash: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

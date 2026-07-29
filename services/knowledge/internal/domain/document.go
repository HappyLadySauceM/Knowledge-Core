package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	StatusDraft     = "draft"
	StatusPublished = "published"
)

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validate %s: %s", e.Field, e.Reason)
}

type Document struct {
	ID             int64
	Title          string
	Summary        string
	Slug           string
	Status         string
	AuthorID       int64
	CurrentVersion int64
	PublishedAt    *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Block struct {
	BlockID     string    `json:"block_id"`
	DocumentID  int64     `json:"document_id"`
	PositionKey string    `json:"position_key"`
	Type        string    `json:"type"`
	ContentJSON string    `json:"content_json"`
	TextContent string    `json:"text_content"`
	Version     int64     `json:"version"`
	UpdatedBy   int64     `json:"updated_by"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Detail struct {
	Document *Document
	Blocks   []*Block
}

type List struct {
	Items    []*Document
	Total    int64
	Page     int
	PageSize int
}

type Operation struct {
	DocumentID          int64
	OperationID         string
	BaseDocumentVersion int64
	BlockID             string
	BaseBlockVersion    int64
	PositionKey         string
	ContentJSON         string
	TextContent         string
	ActorID             int64
}

type OperationAck struct {
	DocumentID      int64
	OperationID     string
	DocumentVersion int64
	BlockVersion    int64
	Duplicate       bool
}

func NewDocument(title, summary string, authorID int64) (*Document, error) {
	if authorID <= 0 {
		return nil, &ValidationError{Field: "author_id", Reason: "must be positive"}
	}
	title, summary, err := NormalizeMetadata(title, summary)
	if err != nil {
		return nil, err
	}
	return &Document{Title: title, Summary: summary, Status: StatusDraft, AuthorID: authorID}, nil
}

func NormalizeMetadata(title, summary string) (string, string, error) {
	title = strings.TrimSpace(title)
	summary = strings.TrimSpace(summary)
	switch {
	case title == "":
		return "", "", &ValidationError{Field: "title", Reason: "is required"}
	case len([]rune(title)) > 200:
		return "", "", &ValidationError{Field: "title", Reason: "must not exceed 200 characters"}
	case len([]rune(summary)) > 1000:
		return "", "", &ValidationError{Field: "summary", Reason: "must not exceed 1000 characters"}
	}
	return title, summary, nil
}

func ValidateStatus(status string) error {
	if status != StatusDraft && status != StatusPublished {
		return &ValidationError{Field: "status", Reason: "must be draft or published"}
	}
	return nil
}

func (o Operation) Validate() error {
	switch {
	case o.DocumentID <= 0:
		return &ValidationError{Field: "document_id", Reason: "must be positive"}
	case o.ActorID <= 0:
		return &ValidationError{Field: "actor_id", Reason: "must be positive"}
	case o.OperationID == "" || len(o.OperationID) > 64:
		return &ValidationError{Field: "op_id", Reason: "must contain between 1 and 64 characters"}
	case o.BaseDocumentVersion < 0:
		return &ValidationError{Field: "base_document_version", Reason: "must not be negative"}
	case o.BlockID == "" || len(o.BlockID) > 64:
		return &ValidationError{Field: "block_id", Reason: "must contain between 1 and 64 characters"}
	case o.BaseBlockVersion < 0:
		return &ValidationError{Field: "base_block_version", Reason: "must not be negative"}
	case strings.TrimSpace(o.PositionKey) == "" || len(o.PositionKey) > 256:
		return &ValidationError{Field: "position_key", Reason: "must contain between 1 and 256 characters"}
	case len(o.ContentJSON) == 0 || len(o.ContentJSON) > 65536:
		return &ValidationError{Field: "content_json", Reason: "must contain between 1 and 65536 bytes"}
	case len(o.TextContent) > 65536:
		return &ValidationError{Field: "text_content", Reason: "must not exceed 65536 bytes"}
	}
	return nil
}

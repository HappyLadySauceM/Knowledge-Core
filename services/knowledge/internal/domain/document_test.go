package domain

import (
	"errors"
	"testing"
)

func TestNewDocumentNormalizesMetadata(t *testing.T) {
	document, err := NewDocument("  First note  ", "  Summary  ", 42)
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	if document.Title != "First note" || document.Summary != "Summary" || document.Status != StatusDraft || document.AuthorID != 42 {
		t.Fatalf("NewDocument() = %#v", document)
	}
}

func TestOperationValidation(t *testing.T) {
	operation := Operation{
		DocumentID: 1, OperationID: "op-1", BaseDocumentVersion: 0,
		BlockID: "block-1", BaseBlockVersion: 0, PositionKey: "a",
		ContentJSON: `{"text":"hello"}`, ActorID: 42,
	}
	if err := operation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	operation.ContentJSON = ""
	err := operation.Validate()
	var validationError *ValidationError
	if !errors.As(err, &validationError) || validationError.Field != "content_json" {
		t.Fatalf("Validate() error = %v", err)
	}
}

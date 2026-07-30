package apperror_test

import (
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
)

var errDocumentNotFound = apperror.MustDefine(
	"knowledge.document_not_found",
	apperror.KindNotFound,
	"document not found",
)

func TestDefinitionWrapPreservesSafeContractAndCause(t *testing.T) {
	cause := errors.New("database address and query must remain internal")
	err := errDocumentNotFound.Wrap(cause)

	if err.Error() != "document not found" {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(err, errDocumentNotFound) || !errors.Is(err, cause) {
		t.Fatal("wrapped error did not preserve definition and cause identity")
	}
	var appError *apperror.Error
	if !errors.As(err, &appError) {
		t.Fatal("errors.As() did not find *apperror.Error")
	}
	if apperror.Key(err) != "knowledge.document_not_found" || apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("Details() = key %q, kind %q", apperror.Key(err), apperror.KindOf(err))
	}
	if apperror.Cause(err) != cause {
		t.Fatal("Cause() did not return the wrapped cause")
	}
}

func TestDefinitionCanBeUsedAsSentinel(t *testing.T) {
	var err error = errDocumentNotFound
	if !errors.Is(err, errDocumentNotFound) {
		t.Fatal("definition cannot be used as an errors.Is sentinel")
	}
	if apperror.SafeMessage(err) != "document not found" {
		t.Fatalf("SafeMessage() = %q", apperror.SafeMessage(err))
	}
}

func TestDefineRejectsUnsafeDefinitions(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		kind    apperror.Kind
		message string
	}{
		{name: "invalid key", key: "Knowledge Missing", kind: apperror.KindNotFound, message: "missing"},
		{name: "invalid kind", key: "knowledge.missing", kind: "other", message: "missing"},
		{name: "empty message", key: "knowledge.missing", kind: apperror.KindNotFound},
		{name: "multiline message", key: "knowledge.missing", kind: apperror.KindNotFound, message: "missing\ndetail"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := apperror.Define(test.key, test.kind, test.message); err == nil {
				t.Fatal("Define() accepted an invalid definition")
			}
		})
	}
}

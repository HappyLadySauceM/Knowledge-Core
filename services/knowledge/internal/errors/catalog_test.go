package errors_test

import (
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
)

func TestCatalog(t *testing.T) {
	tests := []struct {
		mapping knowledgeerrors.Mapping
		code    int32
		key     string
		kind    apperror.Kind
		message string
	}{
		{knowledgeerrors.InvalidInput, knowledgerpc.CodeInvalidInput, "knowledge.invalid_input", apperror.KindInvalidArgument, "invalid request"},
		{knowledgeerrors.NotFound, knowledgerpc.CodeNotFound, "knowledge.not_found", apperror.KindNotFound, "document not found"},
		{knowledgeerrors.Conflict, knowledgerpc.CodeConflict, "knowledge.conflict", apperror.KindConflict, "document version conflict"},
		{knowledgeerrors.Forbidden, knowledgerpc.CodeForbidden, "knowledge.forbidden", apperror.KindPermissionDenied, "permission denied"},
		{knowledgeerrors.Internal, knowledgerpc.CodeInternal, "knowledge.internal", apperror.KindInternal, "internal service error"},
	}

	for _, test := range tests {
		definition := test.mapping.Definition()
		if test.mapping.Code() != test.code || definition.Key() != test.key || definition.Kind() != test.kind || definition.SafeMessage() != test.message {
			t.Fatalf("mapping %q = code %d, kind %q, message %q", definition.Key(), test.mapping.Code(), definition.Kind(), definition.SafeMessage())
		}
	}
}

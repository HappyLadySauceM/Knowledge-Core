package errors

import (
	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
)

type Mapping struct {
	code       int32
	definition apperror.Definition
}

func (m Mapping) Code() int32                     { return m.code }
func (m Mapping) Definition() apperror.Definition { return m.definition }

func define(code int32, key string, kind apperror.Kind, safeMessage string) Mapping {
	return Mapping{code: code, definition: apperror.MustDefine(key, kind, safeMessage)}
}

var (
	InvalidInput = define(knowledgerpc.CodeInvalidInput,
		"knowledge.invalid_input", apperror.KindInvalidArgument, "invalid request")
	NotFound = define(knowledgerpc.CodeNotFound,
		"knowledge.not_found", apperror.KindNotFound, "document not found")
	Conflict = define(knowledgerpc.CodeConflict,
		"knowledge.conflict", apperror.KindConflict, "document version conflict")
	Forbidden = define(knowledgerpc.CodeForbidden,
		"knowledge.forbidden", apperror.KindPermissionDenied, "permission denied")
	Internal = define(knowledgerpc.CodeInternal,
		"knowledge.internal", apperror.KindInternal, "internal service error")
)

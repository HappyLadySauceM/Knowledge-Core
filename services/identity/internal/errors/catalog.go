package errors

import (
	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
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
	InvalidInput = define(identityrpc.CodeInvalidInput,
		"identity.invalid_input", apperror.KindInvalidArgument, "invalid request")
	Conflict = define(identityrpc.CodeConflict,
		"identity.conflict", apperror.KindConflict, "account already exists")
	InvalidCredentials = define(identityrpc.CodeInvalidCredentials,
		"identity.invalid_credentials", apperror.KindUnauthenticated, "invalid credentials")
	AccountLocked = define(identityrpc.CodeAccountLocked,
		"identity.account_locked", apperror.KindPermissionDenied, "account is temporarily locked")
	UserDisabled = define(identityrpc.CodeUserDisabled,
		"identity.user_disabled", apperror.KindPermissionDenied, "account is disabled")
	UserNotFound = define(identityrpc.CodeUserNotFound,
		"identity.user_not_found", apperror.KindNotFound, "user not found")
	Unauthenticated = define(identityrpc.CodeUnauthenticated,
		"identity.unauthenticated", apperror.KindUnauthenticated, "authentication required")
	Forbidden = define(identityrpc.CodeForbidden,
		"identity.forbidden", apperror.KindPermissionDenied, "permission denied")
	Internal = define(identityrpc.CodeInternal,
		"identity.internal", apperror.KindInternal, "internal service error")
)

package errors_test

import (
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	identityrpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
)

func TestCatalog(t *testing.T) {
	tests := []struct {
		mapping identityerrors.Mapping
		code    int32
		key     string
		kind    apperror.Kind
		message string
	}{
		{identityerrors.InvalidInput, identityrpc.CodeInvalidInput, "identity.invalid_input", apperror.KindInvalidArgument, "invalid request"},
		{identityerrors.Conflict, identityrpc.CodeConflict, "identity.conflict", apperror.KindConflict, "account already exists"},
		{identityerrors.InvalidCredentials, identityrpc.CodeInvalidCredentials, "identity.invalid_credentials", apperror.KindUnauthenticated, "invalid credentials"},
		{identityerrors.AccountLocked, identityrpc.CodeAccountLocked, "identity.account_locked", apperror.KindPermissionDenied, "account is temporarily locked"},
		{identityerrors.UserDisabled, identityrpc.CodeUserDisabled, "identity.user_disabled", apperror.KindPermissionDenied, "account is disabled"},
		{identityerrors.UserNotFound, identityrpc.CodeUserNotFound, "identity.user_not_found", apperror.KindNotFound, "user not found"},
		{identityerrors.Unauthenticated, identityrpc.CodeUnauthenticated, "identity.unauthenticated", apperror.KindUnauthenticated, "authentication required"},
		{identityerrors.Forbidden, identityrpc.CodeForbidden, "identity.forbidden", apperror.KindPermissionDenied, "permission denied"},
		{identityerrors.Internal, identityrpc.CodeInternal, "identity.internal", apperror.KindInternal, "internal service error"},
	}

	for _, test := range tests {
		definition := test.mapping.Definition()
		if test.mapping.Code() != test.code || definition.Key() != test.key || definition.Kind() != test.kind || definition.SafeMessage() != test.message {
			t.Fatalf("mapping %q = code %d, kind %q, message %q", definition.Key(), test.mapping.Code(), definition.Kind(), definition.SafeMessage())
		}
	}
}

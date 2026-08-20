// Package errors defines Identity's stable, transport-safe error catalog.
package errors

import apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"

var (
	InvalidInput = apperror.MustDefine(
		20001,
		"identity.invalid_input",
		apperror.KindInvalidArgument,
		"invalid identity input",
	)
	UsernameConflict = apperror.MustDefine(
		20002,
		"identity.username_conflict",
		apperror.KindConflict,
		"username already exists",
	)
	EmailConflict = apperror.MustDefine(
		20002,
		"identity.email_conflict",
		apperror.KindConflict,
		"email already exists",
	)
	InvalidCredentials = apperror.MustDefine(
		20003,
		"identity.invalid_credentials",
		apperror.KindUnauthenticated,
		"invalid credentials",
	)
	AccountLocked = apperror.MustDefine(
		20004,
		"identity.account_locked",
		apperror.KindPermissionDenied,
		"account is locked",
	)
	UserDisabled = apperror.MustDefine(
		20005,
		"identity.user_disabled",
		apperror.KindPermissionDenied,
		"user is disabled",
	)
	UserNotFound = apperror.MustDefine(
		20006,
		"identity.user_not_found",
		apperror.KindNotFound,
		"user not found",
	)
	Unauthenticated = apperror.MustDefine(
		20007,
		"identity.unauthenticated",
		apperror.KindUnauthenticated,
		"authentication is required",
	)
	Forbidden = apperror.MustDefine(
		20008,
		"identity.forbidden",
		apperror.KindPermissionDenied,
		"access is forbidden",
	)
	EmailNotVerified = apperror.MustDefine(20010, "identity.email_not_verified", apperror.KindPermissionDenied, "email verification is required")
	Unimplemented    = apperror.MustDefine(
		20009,
		"identity.unimplemented",
		apperror.KindUnimplemented,
		"identity operation is not implemented",
	)
	Internal = apperror.MustDefine(
		20999,
		"identity.internal",
		apperror.KindInternal,
		"internal identity service error",
	)
)

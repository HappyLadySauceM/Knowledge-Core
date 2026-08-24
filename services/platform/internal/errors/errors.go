package errors

import apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"

var (
	InvalidInput       = apperror.MustDefine(32001, "platform.invalid_input", apperror.KindInvalidArgument, "configuration input is invalid")
	NotFound           = apperror.MustDefine(32002, "platform.not_found", apperror.KindNotFound, "configuration was not found")
	Conflict           = apperror.MustDefine(32003, "platform.conflict", apperror.KindConflict, "configuration update conflicts with existing state")
	Forbidden          = apperror.MustDefine(32004, "platform.forbidden", apperror.KindPermissionDenied, "administrator access is required")
	Unauthenticated    = apperror.MustDefine(32005, "platform.unauthenticated", apperror.KindUnauthenticated, "authentication is required")
	Unavailable        = apperror.MustDefine(32006, "platform.unavailable", apperror.KindUnavailable, "platform service is unavailable")
	PreconditionFailed = apperror.MustDefine(32007, "platform.precondition_failed", apperror.KindConflict, "configuration revision does not match")
	Internal           = apperror.MustDefine(32999, "platform.internal", apperror.KindInternal, "internal server error")
)

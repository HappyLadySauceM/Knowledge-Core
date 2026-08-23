package errors

import apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"

var (
	InvalidInput    = apperror.MustDefine(31001, "attachment.invalid_input", apperror.KindInvalidArgument, "attachment input is invalid")
	NotFound        = apperror.MustDefine(31002, "attachment.not_found", apperror.KindNotFound, "attachment not found")
	Conflict        = apperror.MustDefine(31003, "attachment.conflict", apperror.KindConflict, "attachment state conflict")
	Unauthenticated = apperror.MustDefine(31005, "attachment.unauthenticated", apperror.KindUnauthenticated, "authentication is required")
	Unavailable     = apperror.MustDefine(31006, "attachment.unavailable", apperror.KindUnavailable, "attachment service is unavailable")
	QuotaExceeded   = apperror.MustDefine(31009, "attachment.quota_exceeded", apperror.KindRateLimited, "attachment quota exceeded")
	Internal        = apperror.MustDefine(31999, "attachment.internal", apperror.KindInternal, "internal server error")
)

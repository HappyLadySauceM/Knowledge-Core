package errors

import (
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
)

var (
	InvalidInput    = apperror.MustDefine(knowledgev1.CodeInvalidInput, "knowledge.invalid_input", apperror.KindInvalidArgument, "invalid knowledge input")
	NotFound        = apperror.MustDefine(knowledgev1.CodeNotFound, "knowledge.not_found", apperror.KindNotFound, "document not found")
	Conflict        = apperror.MustDefine(knowledgev1.CodeConflict, "knowledge.conflict", apperror.KindConflict, "resource conflict")
	Forbidden       = apperror.MustDefine(knowledgev1.CodeForbidden, "knowledge.forbidden", apperror.KindPermissionDenied, "permission denied")
	Unauthenticated = apperror.MustDefine(knowledgev1.CodeUnauthenticated, "knowledge.unauthenticated", apperror.KindUnauthenticated, "authentication required")
	Unavailable     = apperror.MustDefine(knowledgev1.CodeUnavailable, "knowledge.unavailable", apperror.KindUnavailable, "dependency unavailable")
	Precondition    = apperror.MustDefine(knowledgev1.CodePreconditionFailed, "knowledge.precondition_failed", apperror.KindConflict, "resource revision does not match")
	Gone            = apperror.MustDefine(knowledgev1.CodeGone, "knowledge.gone", apperror.KindNotFound, "resource is permanently unavailable")
	QuotaExceeded   = apperror.MustDefine(knowledgev1.CodeQuotaExceeded, "knowledge.quota_exceeded", apperror.KindConflict, "storage quota exceeded")
	Internal        = apperror.MustDefine(knowledgev1.CodeInternal, "knowledge.internal", apperror.KindInternal, "internal server error")
)

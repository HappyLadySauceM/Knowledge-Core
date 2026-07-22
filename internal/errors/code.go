package errors

import (
	"net/http"
)

const (
	MessageOK              = "ok"
	MessageInvalidRequest  = "invalid_request"
	MessageUnauthorized    = "unauthorized"
	MessageForbidden       = "forbidden"
	MessageNotFound        = "not_found"
	MessageConflict        = "conflict"
	MessageInternalError   = "internal_error"
	CodeInvalidRequest     = "invalid_request"
	CodeInvalidCredentials = "invalid_credentials"
	CodeUserDisabled       = "user_disabled"
	CodeUserLocked         = "user_locked"
	CodeConflict           = "conflict"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeInvalidToken           = "invalid_token"
	CodeInternalError          = "internal_error"
	CodePasswordChangeRequired = "password_change_required"
)

var (
	InvalidRequest     = New(CodeInvalidRequest, http.StatusBadRequest, MessageInvalidRequest)
	InvalidCredentials = New(CodeInvalidCredentials, http.StatusUnauthorized, MessageUnauthorized)
	UserDisabled       = New(CodeUserDisabled, http.StatusUnauthorized, MessageUnauthorized)
	UserLocked         = New(CodeUserLocked, http.StatusTooManyRequests, "too_many_requests")
	Conflict           = New(CodeConflict, http.StatusConflict, MessageConflict)
	Forbidden          = New(CodeForbidden, http.StatusForbidden, MessageForbidden)
	NotFound           = New(CodeNotFound, http.StatusNotFound, MessageNotFound)
	InvalidToken             = New(CodeInvalidToken, http.StatusUnauthorized, MessageUnauthorized)
	InternalError            = New(CodeInternalError, http.StatusInternalServerError, MessageInternalError)
	PasswordChangeRequired   = New(CodePasswordChangeRequired, http.StatusForbidden, CodePasswordChangeRequired)
)

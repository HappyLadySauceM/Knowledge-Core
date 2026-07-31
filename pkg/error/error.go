// Package apperror defines transport-independent, safe application errors.
// The directory is named error for discoverability; importers should use the
// declared package name to avoid colliding with Go's built-in error type.
package apperror

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/kitex/pkg/kerrors"
)

type Kind string

const (
	KindInvalidArgument  Kind = "invalid_argument"
	KindUnauthenticated  Kind = "unauthenticated"
	KindPermissionDenied Kind = "permission_denied"
	KindNotFound         Kind = "not_found"
	KindConflict         Kind = "conflict"
	KindRateLimited      Kind = "rate_limited"
	KindDeadlineExceeded Kind = "deadline_exceeded"
	KindUnavailable      Kind = "unavailable"
	KindUnimplemented    Kind = "unimplemented"
	KindInternal         Kind = "internal"
)

const (
	ExtraErrorKey   = "error_key"
	ExtraErrorKind  = "error_kind"
	ExtraRequestID  = "request_id"
	ExtraTraceID    = "trace_id"
	unknownCode     = int32(20999)
	maxSafeMsgBytes = 256
)

var (
	keyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

	// Internal is used when a non-application error crosses a transport
	// boundary. Its cause remains available locally but is never serialized.
	Internal = MustDefine(unknownCode, "common.internal", KindInternal, "internal server error")
)

// Definition is an immutable catalog entry. Exported fields make catalogs
// straightforward to inspect and generate while Define validates all values.
type Definition struct {
	Code    int32
	Key     string
	Kind    Kind
	Message string
}

func Define(code int32, key string, kind Kind, safeMessage string) (Definition, error) {
	key = strings.TrimSpace(key)
	safeMessage = strings.TrimSpace(safeMessage)
	switch {
	case code <= 0:
		return Definition{}, fmt.Errorf("define application error %q: code must be positive", key)
	case !keyPattern.MatchString(key):
		return Definition{}, fmt.Errorf("define application error: invalid key %q", key)
	case !validKind(kind):
		return Definition{}, fmt.Errorf("define application error %q: invalid kind %q", key, kind)
	case safeMessage == "":
		return Definition{}, fmt.Errorf("define application error %q: safe message is required", key)
	case len(safeMessage) > maxSafeMsgBytes || strings.ContainsAny(safeMessage, "\r\n"):
		return Definition{}, fmt.Errorf("define application error %q: safe message must be one line and at most %d bytes", key, maxSafeMsgBytes)
	default:
		return Definition{Code: code, Key: key, Kind: kind, Message: safeMessage}, nil
	}
}

func MustDefine(code int32, key string, kind Kind, safeMessage string) Definition {
	definition, err := Define(code, key, kind, safeMessage)
	if err != nil {
		panic(err)
	}
	return definition
}

func (d Definition) Error() string { return d.Message }

func (d Definition) Is(target error) bool {
	switch candidate := target.(type) {
	case Definition:
		return sameDefinition(d, candidate)
	case *Error:
		return candidate != nil && sameDefinition(d, candidate.definition)
	default:
		return false
	}
}

func (d Definition) New() error { return &Error{definition: d} }

func (d Definition) Wrap(cause error) error {
	return &Error{definition: d, cause: cause}
}

// Error binds a safe definition to an optional internal cause. Error returns
// only the catalog message, preventing accidental transport leakage.
type Error struct {
	definition Definition
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.definition.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	switch candidate := target.(type) {
	case Definition:
		return sameDefinition(e.definition, candidate)
	case *Error:
		return candidate != nil && sameDefinition(e.definition, candidate.definition)
	default:
		return false
	}
}

// As lets errors.As retrieve the definition without exposing mutable internals.
func (e *Error) As(target any) bool {
	if e == nil {
		return false
	}
	definition, ok := target.(*Definition)
	if !ok || definition == nil {
		return false
	}
	*definition = e.definition
	return true
}

func (e *Error) Definition() Definition {
	if e == nil {
		return Definition{}
	}
	return e.definition
}

func Details(err error) (Definition, bool) {
	if err == nil {
		return Definition{}, false
	}
	var definition Definition
	if errors.As(err, &definition) && definition.valid() {
		return definition, true
	}
	return Definition{}, false
}

func Key(err error) string {
	definition, _ := Details(err)
	return definition.Key
}

func KindOf(err error) Kind {
	definition, _ := Details(err)
	return definition.Kind
}

func Code(err error) int32 {
	definition, _ := Details(err)
	return definition.Code
}

func SafeMessage(err error) string {
	definition, _ := Details(err)
	return definition.Message
}

func Cause(err error) error {
	var appError *Error
	if errors.As(err, &appError) && appError != nil {
		return appError.cause
	}
	return nil
}

func (d Definition) valid() bool {
	return d.Code > 0 &&
		keyPattern.MatchString(d.Key) &&
		validKind(d.Kind) &&
		d.Message != "" &&
		len(d.Message) <= maxSafeMsgBytes &&
		!strings.ContainsAny(d.Message, "\r\n")
}

func sameDefinition(left, right Definition) bool {
	return left.Code > 0 && left.Code == right.Code && left.Key != "" && left.Key == right.Key
}

func validKind(kind Kind) bool {
	switch kind {
	case KindInvalidArgument,
		KindUnauthenticated,
		KindPermissionDenied,
		KindNotFound,
		KindConflict,
		KindRateLimited,
		KindDeadlineExceeded,
		KindUnavailable,
		KindUnimplemented,
		KindInternal:
		return true
	default:
		return false
	}
}

// kitexBizError retains the local cause for errors.Is/As and structured logs.
// Only the safe BizStatus fields are serialized by Kitex.
type kitexBizError struct {
	kerrors.BizStatusErrorIface
	cause error
}

func (e *kitexBizError) Unwrap() error { return e.cause }

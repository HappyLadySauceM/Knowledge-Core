package apperror

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Kind classifies an application error without coupling it to a transport.
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
	KindInternal         Kind = "internal"
)

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

// Definition is an immutable, reusable application error definition. It also
// implements error so callers can keep using errors.Is(err, catalog.Error).
type Definition struct {
	key         string
	kind        Kind
	safeMessage string
}

func Define(key string, kind Kind, safeMessage string) (Definition, error) {
	key = strings.TrimSpace(key)
	safeMessage = strings.TrimSpace(safeMessage)
	switch {
	case !keyPattern.MatchString(key):
		return Definition{}, fmt.Errorf("define application error: invalid key %q", key)
	case !validKind(kind):
		return Definition{}, fmt.Errorf("define application error %q: invalid kind %q", key, kind)
	case safeMessage == "":
		return Definition{}, fmt.Errorf("define application error %q: safe message is required", key)
	case len(safeMessage) > 256 || strings.ContainsAny(safeMessage, "\r\n"):
		return Definition{}, fmt.Errorf("define application error %q: safe message must be one line and at most 256 bytes", key)
	default:
		return Definition{key: key, kind: kind, safeMessage: safeMessage}, nil
	}
}

func MustDefine(key string, kind Kind, safeMessage string) Definition {
	definition, err := Define(key, kind, safeMessage)
	if err != nil {
		panic(err)
	}
	return definition
}

func (d Definition) Key() string         { return d.key }
func (d Definition) Kind() Kind          { return d.kind }
func (d Definition) SafeMessage() string { return d.safeMessage }
func (d Definition) Error() string       { return d.safeMessage }

func (d Definition) New() error {
	return &Error{definition: d}
}

func (d Definition) Wrap(cause error) error {
	return &Error{definition: d, cause: cause}
}

// Error binds a definition to an optional internal cause. Error deliberately
// returns only the safe message so transport code cannot leak the cause by
// serializing err.Error().
type Error struct {
	definition Definition
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.definition.SafeMessage()
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
		return e.definition.key != "" && e.definition.key == candidate.key
	case *Error:
		return candidate != nil && e.definition.key != "" && e.definition.key == candidate.definition.key
	default:
		return false
	}
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
	var appError *Error
	if errors.As(err, &appError) && appError != nil {
		return appError.definition, appError.definition.key != ""
	}
	var definition Definition
	if errors.As(err, &definition) {
		return definition, definition.key != ""
	}
	return Definition{}, false
}

func Key(err error) string {
	definition, _ := Details(err)
	return definition.Key()
}

func KindOf(err error) Kind {
	definition, _ := Details(err)
	return definition.Kind()
}

func SafeMessage(err error) string {
	definition, _ := Details(err)
	return definition.SafeMessage()
}

func Cause(err error) error {
	var appError *Error
	if errors.As(err, &appError) && appError != nil {
		return appError.cause
	}
	return nil
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
		KindInternal:
		return true
	default:
		return false
	}
}

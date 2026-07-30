package rpcerror

import (
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

const (
	extraErrorKey  = "error_key"
	extraErrorKind = "error_kind"
)

// Status is a Kitex business status that retains the local application error
// chain. Kitex serializes only the code, safe message, and safe extras.
type Status struct {
	code       int32
	definition apperror.Definition
	cause      error
}

func New(code int32, definition apperror.Definition, cause error) error {
	if code <= 0 {
		panic("create RPC status: code must be positive")
	}
	if definition.Key() == "" {
		panic("create RPC status: application error definition is required")
	}
	if cause == nil {
		cause = definition.New()
	} else if !errors.Is(cause, definition) {
		cause = definition.Wrap(cause)
	}
	return &Status{code: code, definition: definition, cause: cause}
}

func (e *Status) BizStatusCode() int32 { return e.code }
func (e *Status) BizMessage() string   { return e.definition.SafeMessage() }
func (e *Status) BizExtra() map[string]string {
	return map[string]string{
		extraErrorKey:  e.definition.Key(),
		extraErrorKind: string(e.definition.Kind()),
	}
}

func (e *Status) Error() string {
	return fmt.Sprintf("biz error: code=%d, msg=%s", e.code, e.definition.SafeMessage())
}

func (e *Status) Unwrap() error { return e.cause }

func Metadata(err error) (key string, kind apperror.Kind) {
	bizError, ok := kerrors.FromBizStatusError(err)
	if !ok {
		return "", ""
	}
	extra := bizError.BizExtra()
	return extra[extraErrorKey], apperror.Kind(extra[extraErrorKind])
}

var _ kerrors.BizStatusErrorIface = (*Status)(nil)

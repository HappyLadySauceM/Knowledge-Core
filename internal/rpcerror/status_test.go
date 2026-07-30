package rpcerror_test

import (
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestStatusExposesSafeWireDataAndKeepsLocalCause(t *testing.T) {
	definition := apperror.MustDefine("identity.internal", apperror.KindInternal, "internal service error")
	cause := errors.New("private database detail")
	err := rpcerror.New(20999, definition, cause)

	bizError, ok := kerrors.FromBizStatusError(err)
	if !ok {
		t.Fatal("status does not implement BizStatusErrorIface")
	}
	if bizError.BizStatusCode() != 20999 || bizError.BizMessage() != "internal service error" {
		t.Fatalf("wire status = %d %q", bizError.BizStatusCode(), bizError.BizMessage())
	}
	if key, kind := rpcerror.Metadata(err); key != "identity.internal" || kind != apperror.KindInternal {
		t.Fatalf("metadata = %q %q", key, kind)
	}
	if !errors.Is(err, definition) || !errors.Is(err, cause) {
		t.Fatal("status did not retain its local application cause chain")
	}
	if errors.Is(errors.New(err.Error()), cause) {
		t.Fatal("serialized error unexpectedly retained the private cause")
	}
	if apperror.Cause(err) != cause {
		t.Fatal("status did not expose its local cause to the logging boundary")
	}
}

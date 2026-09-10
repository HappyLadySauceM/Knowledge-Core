package apperror_test

import (
	"context"
	"errors"
	"testing"

	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"go.opentelemetry.io/otel/trace"
)

var errDocumentNotFound = apperror.MustDefine(
	20004,
	"knowledge.document_not_found",
	apperror.KindNotFound,
	"document not found",
)

func TestDefinitionWrapPreservesSafeContractAndCause(t *testing.T) {
	cause := errors.New("database address and query must remain internal")
	err := errDocumentNotFound.Wrap(cause)

	if err.Error() != "document not found" {
		t.Fatalf("Error() = %q", err.Error())
	}
	if !errors.Is(err, errDocumentNotFound) || !errors.Is(err, cause) {
		t.Fatal("wrapped error did not preserve definition and cause identity")
	}
	var appError *apperror.Error
	if !errors.As(err, &appError) {
		t.Fatal("errors.As() did not find *apperror.Error")
	}
	var definition apperror.Definition
	if !errors.As(err, &definition) || definition != errDocumentNotFound {
		t.Fatalf("errors.As() definition = %#v", definition)
	}
	if apperror.Key(err) != errDocumentNotFound.Key || apperror.KindOf(err) != apperror.KindNotFound || apperror.Code(err) != 20004 {
		t.Fatalf("Details() = code %d, key %q, kind %q", apperror.Code(err), apperror.Key(err), apperror.KindOf(err))
	}
	if apperror.Cause(err) != cause {
		t.Fatal("Cause() did not return the wrapped cause")
	}
}

func TestToBizStatusCarriesSafeContextMetadata(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))
	ctx = metadata.WithRequestID(ctx, "request-1")
	cause := errors.New("secret database detail")
	biz := apperror.ToBizStatus(ctx, errDocumentNotFound.Wrap(cause))

	if biz.BizStatusCode() != 20004 || biz.BizMessage() != "document not found" {
		t.Fatalf("business status = %d %q", biz.BizStatusCode(), biz.BizMessage())
	}
	extra := biz.BizExtra()
	if extra[apperror.ExtraErrorKey] != errDocumentNotFound.Key || extra[apperror.ExtraErrorKind] != string(apperror.KindNotFound) {
		t.Fatalf("business status extras = %#v", extra)
	}
	if extra[apperror.ExtraRequestID] != "request-1" || extra[apperror.ExtraTraceID] != traceID.String() {
		t.Fatalf("business status trace extras = %#v", extra)
	}
	if biz.BizMessage() == cause.Error() {
		t.Fatal("business status leaked the internal cause")
	}
	if !errors.Is(biz, cause) {
		t.Fatal("local business error no longer unwraps to its cause")
	}
}

func TestToBizStatusMapsUnknownErrorsToInternal(t *testing.T) {
	biz := apperror.ToBizStatus(context.Background(), errors.New("private detail"))
	if biz.BizStatusCode() != apperror.Internal.Code || biz.BizMessage() != apperror.Internal.Message {
		t.Fatalf("business status = %d %q", biz.BizStatusCode(), biz.BizMessage())
	}
	if biz.BizExtra()[apperror.ExtraErrorKey] != apperror.Internal.Key {
		t.Fatalf("business status extras = %#v", biz.BizExtra())
	}
}

func TestFromKitexBizStatusRoundTripsCatalog(t *testing.T) {
	cause := errors.New("ERROR: value too long for type character varying(16) (SQLSTATE 22001)")
	biz := apperror.ToKitexBizStatus(context.Background(), errDocumentNotFound.Wrap(cause))

	definition, ok := apperror.FromKitexBizStatus(biz)
	if !ok {
		t.Fatal("FromKitexBizStatus() = false, want true")
	}
	if definition != errDocumentNotFound {
		t.Fatalf("FromKitexBizStatus() = %#v, want %#v", definition, errDocumentNotFound)
	}
	if definition.Message == cause.Error() || definition.Message != "document not found" {
		t.Fatalf("reconstructed message leaked cause: %#v", definition)
	}
}

func TestFromKitexBizStatusDistinguishesSharedCodesByKey(t *testing.T) {
	usernameConflict := apperror.MustDefine(20002, "identity.username_conflict", apperror.KindConflict, "username already exists")
	emailConflict := apperror.MustDefine(20002, "identity.email_conflict", apperror.KindConflict, "email already exists")

	username, usernameOK := apperror.FromKitexBizStatus(apperror.ToKitexBizStatus(context.Background(), usernameConflict.New()))
	email, emailOK := apperror.FromKitexBizStatus(apperror.ToKitexBizStatus(context.Background(), emailConflict.New()))
	if !usernameOK || !emailOK {
		t.Fatalf("FromKitexBizStatus() ok = %v, %v", usernameOK, emailOK)
	}
	if username != usernameConflict || email != emailConflict {
		t.Fatalf("shared code reconstructions = %#v, %#v", username, email)
	}
}

func TestFromKitexBizStatusRejectsIncompleteExtras(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "nil", err: nil},
		{name: "plain", err: errors.New("connection refused")},
		{
			name: "missing extras",
			err:  kerrors.NewBizStatusError(20004, "document not found"),
		},
		{
			name: "missing kind",
			err: kerrors.NewBizStatusErrorWithExtra(20004, "document not found", map[string]string{
				apperror.ExtraErrorKey: "knowledge.document_not_found",
			}),
		},
		{
			name: "invalid kind",
			err: kerrors.NewBizStatusErrorWithExtra(20004, "document not found", map[string]string{
				apperror.ExtraErrorKey:  "knowledge.document_not_found",
				apperror.ExtraErrorKind: "other",
			}),
		},
		{
			name: "unsafe message",
			err: kerrors.NewBizStatusErrorWithExtra(20004, "ERROR: value too long for type character varying(16)\nSQLSTATE 22001", map[string]string{
				apperror.ExtraErrorKey:  "knowledge.document_not_found",
				apperror.ExtraErrorKind: string(apperror.KindNotFound),
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if definition, ok := apperror.FromKitexBizStatus(test.err); ok {
				t.Fatalf("FromKitexBizStatus() = %#v, true", definition)
			}
		})
	}
}

func TestDefineRejectsUnsafeDefinitions(t *testing.T) {
	tests := []struct {
		code    int32
		key     string
		kind    apperror.Kind
		message string
	}{
		{key: "knowledge.missing", kind: apperror.KindNotFound, message: "missing"},
		{code: 1, key: "Knowledge Missing", kind: apperror.KindNotFound, message: "missing"},
		{code: 1, key: "knowledge.missing", kind: "other", message: "missing"},
		{code: 1, key: "knowledge.missing", kind: apperror.KindNotFound},
		{code: 1, key: "knowledge.missing", kind: apperror.KindNotFound, message: "missing\ndetail"},
	}
	for _, test := range tests {
		if _, err := apperror.Define(test.code, test.key, test.kind, test.message); err == nil {
			t.Fatalf("Define(%d, %q, %q, %q) accepted invalid input", test.code, test.key, test.kind, test.message)
		}
	}
}

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
	identityv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/identity"
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/circuit"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestCollaborationDeadlineKeepsCatalogKey(t *testing.T) {
	request := app.NewContext(0)
	error := kerrors.NewBizStatusErrorWithExtra(
		collaborationv1.CodeUnavailable,
		"request deadline exceeded",
		map[string]string{
			apperror.ExtraErrorKey:  "collaboration.deadline_exceeded",
			apperror.ExtraErrorKind: string(apperror.KindDeadlineExceeded),
		},
	)

	WriteCollaborationError(context.Background(), request, error)

	problem := decodeProblem(t, request)
	if request.Response.StatusCode() != consts.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", request.Response.StatusCode(), consts.StatusGatewayTimeout)
	}
	if problem.Key != "collaboration.deadline_exceeded" || problem.Detail != "request deadline exceeded" {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestUpstreamInternalErrorsKeepCatalogContract(t *testing.T) {
	cause := errors.New("ERROR: value too long for type character varying(16) (SQLSTATE 22001)")
	tests := []struct {
		name    string
		write   func(context.Context, *app.RequestContext, error)
		catalog apperror.Definition
	}{
		{name: "identity", write: WriteIdentityError, catalog: apperror.MustDefine(identityv1.CodeInternal, "identity.internal", apperror.KindInternal, "internal identity service error")},
		{name: "knowledge", write: WriteKnowledgeError, catalog: apperror.MustDefine(knowledgev1.CodeInternal, "knowledge.internal", apperror.KindInternal, "internal server error")},
		{name: "attachment", write: WriteAttachmentError, catalog: apperror.MustDefine(attachmentv1.CodeInternal, "attachment.internal", apperror.KindInternal, "internal server error")},
		{name: "collaboration", write: WriteCollaborationError, catalog: apperror.MustDefine(collaborationv1.CodeInternal, "collaboration.internal", apperror.KindInternal, "internal server error")},
		{name: "platform", write: WritePlatformError, catalog: apperror.MustDefine(platformv1.CodeInternal, "platform.internal", apperror.KindInternal, "internal server error")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := app.NewContext(0)
			test.write(context.Background(), request, apperror.ToKitexBizStatus(context.Background(), test.catalog.Wrap(cause)))
			problem := decodeProblem(t, request)
			if request.Response.StatusCode() != consts.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", request.Response.StatusCode(), consts.StatusInternalServerError)
			}
			if problem.Key != test.catalog.Key || problem.Detail != test.catalog.Message || problem.Code != test.catalog.Code {
				t.Fatalf("problem = %#v, want key %q detail %q", problem, test.catalog.Key, test.catalog.Message)
			}
			if strings.Contains(string(request.Response.Body()), "SQLSTATE") || strings.Contains(problem.Detail, "varchar") {
				t.Fatalf("problem leaked SQL cause: %#v", problem)
			}
			if strings.Contains(problem.Detail, "unavailable") {
				t.Fatalf("internal error was rewritten as unavailable: %#v", problem)
			}
		})
	}
}

func TestWriteRPCErrorPreservesCatalogKeysAndProtocolStatus(t *testing.T) {
	tests := []struct {
		name    string
		write   func(context.Context, *app.RequestContext, error)
		catalog apperror.Definition
		status  int
	}{
		{
			name:    "username conflict",
			write:   WriteIdentityError,
			catalog: apperror.MustDefine(identityv1.CodeConflict, "identity.username_conflict", apperror.KindConflict, "username already exists"),
			status:  consts.StatusConflict,
		},
		{
			name:    "email conflict",
			write:   WriteIdentityError,
			catalog: apperror.MustDefine(identityv1.CodeConflict, "identity.email_conflict", apperror.KindConflict, "email already exists"),
			status:  consts.StatusConflict,
		},
		{
			name:    "account locked",
			write:   WriteIdentityError,
			catalog: apperror.MustDefine(identityv1.CodeAccountLocked, "identity.account_locked", apperror.KindPermissionDenied, "account is locked"),
			status:  consts.StatusLocked,
		},
		{
			name:    "unimplemented",
			write:   WriteIdentityError,
			catalog: apperror.MustDefine(20009, "identity.unimplemented", apperror.KindUnimplemented, "identity operation is not implemented"),
			status:  consts.StatusNotImplemented,
		},
		{
			name:    "attachment quota",
			write:   WriteAttachmentError,
			catalog: apperror.MustDefine(attachmentv1.CodeQuotaExceeded, "attachment.quota_exceeded", apperror.KindRateLimited, "attachment quota exceeded"),
			status:  consts.StatusTooManyRequests,
		},
		{
			name:    "knowledge unavailable",
			write:   WriteKnowledgeError,
			catalog: apperror.MustDefine(knowledgev1.CodeUnavailable, "knowledge.unavailable", apperror.KindUnavailable, "dependency unavailable"),
			status:  consts.StatusServiceUnavailable,
		},
		{
			name:    "collaboration unavailable",
			write:   WriteCollaborationError,
			catalog: apperror.MustDefine(collaborationv1.CodeUnavailable, "collaboration.unavailable", apperror.KindUnavailable, "dependency unavailable"),
			status:  consts.StatusServiceUnavailable,
		},
		{
			name:    "knowledge gone",
			write:   WriteKnowledgeError,
			catalog: apperror.MustDefine(knowledgev1.CodeGone, "knowledge.gone", apperror.KindNotFound, "resource is permanently unavailable"),
			status:  consts.StatusGone,
		},
		{
			name:    "knowledge precondition",
			write:   WriteKnowledgeError,
			catalog: apperror.MustDefine(knowledgev1.CodePreconditionFailed, "knowledge.precondition_failed", apperror.KindConflict, "resource revision does not match"),
			status:  consts.StatusPreconditionFailed,
		},
		{
			name:    "collaboration precondition",
			write:   WriteCollaborationError,
			catalog: apperror.MustDefine(collaborationv1.CodePreconditionFailed, "collaboration.precondition_failed", apperror.KindConflict, "document sequence does not match"),
			status:  consts.StatusPreconditionFailed,
		},
		{
			name:    "platform precondition",
			write:   WritePlatformError,
			catalog: apperror.MustDefine(platformv1.CodePreconditionFailed, "platform.precondition_failed", apperror.KindConflict, "configuration revision does not match"),
			status:  consts.StatusPreconditionFailed,
		},
		{
			name:    "knowledge not found",
			write:   WriteKnowledgeError,
			catalog: apperror.MustDefine(knowledgev1.CodeNotFound, "knowledge.not_found", apperror.KindNotFound, "document not found"),
			status:  consts.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := app.NewContext(0)
			test.write(context.Background(), request, apperror.ToKitexBizStatus(context.Background(), test.catalog.New()))
			problem := decodeProblem(t, request)
			if request.Response.StatusCode() != test.status {
				t.Fatalf("status = %d, want %d body = %s", request.Response.StatusCode(), test.status, request.Response.Body())
			}
			if problem.Key != test.catalog.Key || problem.Detail != test.catalog.Message || problem.Code != test.catalog.Code {
				t.Fatalf("problem = %#v", problem)
			}
		})
	}
}

func TestWriteIdentityErrorSetsRetryAfter(t *testing.T) {
	catalog := apperror.MustDefine(identityv1.CodeVerificationCooldown, "identity.verification_cooldown", apperror.KindRateLimited, "verification email was recently sent")
	request := app.NewContext(0)
	WriteIdentityError(context.Background(), request, apperror.ToKitexBizStatus(context.Background(), catalog.NewWithExtra(map[string]string{apperror.ExtraRetryAfter: "1800"})))
	problem := decodeProblem(t, request)
	if request.Response.StatusCode() != consts.StatusTooManyRequests {
		t.Fatalf("status = %d, body = %s", request.Response.StatusCode(), request.Response.Body())
	}
	if problem.Key != catalog.Key || problem.RetryAfter != "1800" {
		t.Fatalf("problem = %#v", problem)
	}
	if got := string(request.Response.Header.Peek("Retry-After")); got != "1800" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestInvalidBizStatusMapsToInvalidUpstreamResponse(t *testing.T) {
	request := app.NewContext(0)
	WriteIdentityError(context.Background(), request, kerrors.NewBizStatusError(identityv1.CodeInternal, "identity service unavailable"))
	problem := decodeProblem(t, request)
	if request.Response.StatusCode() != consts.StatusBadGateway || problem.Key != "gateway.invalid_upstream_response" {
		t.Fatalf("status = %d problem = %#v", request.Response.StatusCode(), problem)
	}
}

func TestCircuitOpenMapsToDependencyUnavailable(t *testing.T) {
	writers := []struct {
		name  string
		write func(context.Context, *app.RequestContext, error)
	}{
		{name: "identity", write: WriteIdentityError},
		{name: "knowledge", write: WriteKnowledgeError},
		{name: "collaboration", write: WriteCollaborationError},
	}
	for _, writer := range writers {
		t.Run(writer.name, func(t *testing.T) {
			request := app.NewContext(0)
			writer.write(context.Background(), request, circuit.ErrOpen)
			if status := request.Response.StatusCode(); status != consts.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", status, consts.StatusServiceUnavailable)
			}
			var problem apperror.HTTPProblem
			if err := json.Unmarshal(request.Response.Body(), &problem); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if problem.Key != "gateway.dependency_unavailable" {
				t.Fatalf("key = %q, want gateway.dependency_unavailable", problem.Key)
			}
		})
	}
}

func decodeProblem(t *testing.T, request *app.RequestContext) apperror.HTTPProblem {
	t.Helper()
	var problem apperror.HTTPProblem
	if err := json.Unmarshal(request.Response.Body(), &problem); err != nil {
		t.Fatalf("decode problem: %v body = %s", err, request.Response.Body())
	}
	return problem
}

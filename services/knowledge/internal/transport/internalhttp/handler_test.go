package internalhttp

import (
	"context"
	"errors"
	"testing"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgelogic "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/logic"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/cloudwego/hertz/pkg/route/param"
)

const testDocumentID = "0198a3c0-0000-7000-8000-000000000001"

type collaborationServiceStub struct {
	authorization *knowledgelogic.CollaborationAuthorization
	authorizeErr  error
	projectErr    error
	actorID       int64
	sequence      int64
	plainText     string
}

func (s *collaborationServiceStub) Authorize(_ context.Context, _ string, actorID int64) (*knowledgelogic.CollaborationAuthorization, error) {
	s.actorID = actorID
	return s.authorization, s.authorizeErr
}

func (s *collaborationServiceStub) Project(_ context.Context, _ string, sequence int64, _ domain.RichTextDocument, plainText string) error {
	s.sequence = sequence
	s.plainText = plainText
	return s.projectErr
}

type verifierStub struct {
	principal coreauth.Principal
	err       error
}

func (s verifierStub) Verify(string) (coreauth.Principal, error) { return s.principal, s.err }

func TestAuthorizeReturnsAnonymousAndAuthenticatedContracts(t *testing.T) {
	service := &collaborationServiceStub{authorization: &knowledgelogic.CollaborationAuthorization{
		Document: &domain.Document{ID: testDocumentID, Access: domain.AccessViewer, PermissionRevision: 3},
	}}
	handler, _ := NewHandler(service, verifierStub{})
	request := internalRequest("")
	handler.Authorize(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusOK || service.actorID != 0 {
		t.Fatalf("anonymous status = %d, actor = %d", request.Response.StatusCode(), service.actorID)
	}
	var anonymous authorizationResponse
	if err := jsoncodec.Unmarshal(request.Response.Body(), &anonymous); err != nil {
		t.Fatal(err)
	}
	if anonymous.User != nil || anonymous.TokenExpiresAt != nil || anonymous.PermissionRevision != 3 {
		t.Fatalf("anonymous response = %#v", anonymous)
	}

	expiresAt := time.Date(2026, time.August, 2, 12, 15, 0, 0, time.UTC)
	service.authorization.User = &domain.PublicUser{ID: 42, Username: "alice"}
	handler, _ = NewHandler(service, verifierStub{principal: coreauth.Principal{UserID: 42, ExpiresAt: expiresAt}})
	request = internalRequest("Bearer signed-token")
	handler.Authorize(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusOK || service.actorID != 42 {
		t.Fatalf("authenticated status = %d, actor = %d", request.Response.StatusCode(), service.actorID)
	}
	var authenticated authorizationResponse
	if err := jsoncodec.Unmarshal(request.Response.Body(), &authenticated); err != nil {
		t.Fatal(err)
	}
	if authenticated.User == nil || authenticated.User.ID != 42 || authenticated.TokenExpiresAt == nil || *authenticated.TokenExpiresAt != expiresAt.Format(time.RFC3339Nano) {
		t.Fatalf("authenticated response = %#v", authenticated)
	}
}

func TestAuthorizeRejectsInvalidBearerWithProblemJSON(t *testing.T) {
	handler, _ := NewHandler(&collaborationServiceStub{}, verifierStub{err: errors.New("bad token")})
	request := internalRequest("Bearer invalid")
	handler.Authorize(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusUnauthorized {
		t.Fatalf("status = %d", request.Response.StatusCode())
	}
	if contentType := string(request.Response.Header.ContentType()); contentType != apperror.ProblemContentType {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestProjectUsesStrictJSONAndForwardsValidProjection(t *testing.T) {
	service := &collaborationServiceStub{}
	handler, _ := NewHandler(service, verifierStub{})
	request := internalRequest("")
	request.Request.Header.SetContentTypeBytes([]byte(consts.MIMEApplicationJSON))
	request.Request.SetBodyString(`{"sequence":7,"content":{"type":"doc","content":[{"type":"paragraph"}]},"plain_text":"hello","unknown":true}`)
	handler.Project(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusBadRequest || service.sequence != 0 {
		t.Fatalf("strict status = %d, sequence = %d", request.Response.StatusCode(), service.sequence)
	}

	request = internalRequest("")
	request.Request.Header.SetContentTypeBytes([]byte(consts.MIMEApplicationJSON))
	request.Request.SetBodyString(`{"sequence":7,"content":{"type":"doc","content":[{"type":"paragraph"}]},"plain_text":"hello"}`)
	handler.Project(context.Background(), request)
	if request.Response.StatusCode() != consts.StatusNoContent || service.sequence != 7 || service.plainText != "hello" {
		t.Fatalf("status = %d, sequence = %d, plainText = %q", request.Response.StatusCode(), service.sequence, service.plainText)
	}
}

func internalRequest(authorization string) *app.RequestContext {
	request := app.NewContext(0)
	request.Params = param.Params{{Key: "document_id", Value: testDocumentID}}
	if authorization != "" {
		request.Request.Header.Set("Authorization", authorization)
	}
	return request
}

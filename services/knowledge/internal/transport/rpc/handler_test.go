package rpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	knowledgelogic "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/logic"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type documentServiceStub struct {
	page     knowledgelogic.DocumentPage
	document *domain.Document
	err      error
	actorID  int64
	getCalls int
}

func (s *documentServiceStub) ListPublished(context.Context, knowledgelogic.ListDocumentsInput) (knowledgelogic.DocumentPage, error) {
	return s.page, s.err
}
func (s *documentServiceStub) GetPublished(context.Context, string, int64) (*knowledgelogic.DocumentDetail, error) {
	return nil, s.err
}
func (s *documentServiceStub) List(context.Context, knowledgelogic.ListDocumentsInput) (knowledgelogic.DocumentPage, error) {
	return s.page, s.err
}
func (s *documentServiceStub) ListDeleted(context.Context, knowledgelogic.ListDocumentsInput) (knowledgelogic.DocumentPage, error) {
	return s.page, s.err
}
func (s *documentServiceStub) Create(context.Context, knowledgelogic.CreateDocumentInput) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) Get(_ context.Context, _ string, actorID int64) (*domain.Document, error) {
	s.actorID = actorID
	s.getCalls++
	return s.document, s.err
}
func (s *documentServiceStub) Update(context.Context, knowledgelogic.UpdateDocumentInput) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) SetPublication(context.Context, string, int64, int64, bool) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) Delete(context.Context, string, int64, int64) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) Restore(context.Context, string, int64) (*domain.Document, error) {
	return s.document, s.err
}

type memberServiceStub struct{}

func (*memberServiceStub) List(context.Context, string, int64) ([]*domain.Member, error) {
	return nil, nil
}
func (*memberServiceStub) Add(context.Context, knowledgelogic.AddMemberInput) (*domain.Member, error) {
	return nil, nil
}
func (*memberServiceStub) Update(context.Context, string, int64, int64, int64, string) (*domain.Member, error) {
	return nil, nil
}
func (*memberServiceStub) Delete(context.Context, string, int64, int64, int64) error { return nil }

type attachmentServiceStub struct{}

func (*attachmentServiceStub) List(context.Context, string, int64) ([]*domain.Attachment, error) {
	return nil, nil
}
func (*attachmentServiceStub) Create(context.Context, knowledgelogic.CreateAttachmentInput) (*domain.AttachmentUpload, error) {
	return nil, nil
}
func (*attachmentServiceStub) Complete(context.Context, string, string, int64) (*domain.Attachment, error) {
	return nil, nil
}
func (*attachmentServiceStub) Delete(context.Context, string, string, int64) error { return nil }
func (*attachmentServiceStub) Content(context.Context, string, int64) (*domain.AttachmentContent, error) {
	return nil, nil
}

type tokenVerifierStub struct {
	principal coreauth.Principal
	err       error
}

func (s tokenVerifierStub) Verify(string) (coreauth.Principal, error) { return s.principal, s.err }

type readinessStub struct{ err error }

func (s readinessStub) Ready(context.Context) error { return s.err }

func TestListPublishedDocumentsAllowsAnonymousAndUsesDirectResource(t *testing.T) {
	document := completeDocument()
	documents := &documentServiceStub{page: knowledgelogic.DocumentPage{Items: []*domain.Document{document}, HasMore: false}}
	handler := newTestHandler(t, documents, tokenVerifierStub{})
	response, err := handler.ListPublishedDocuments(context.Background(), &knowledgev1.ListDocumentsRequest{})
	if err != nil {
		t.Fatalf("ListPublishedDocuments() error = %v", err)
	}
	if response == nil || len(response.Items) != 1 || response.Items[0].Id != document.ID || response.Page == nil {
		t.Fatalf("response = %#v", response)
	}
	if _, err := time.Parse(time.RFC3339Nano, response.Items[0].CreatedAt); err != nil {
		t.Fatalf("CreatedAt = %q", response.Items[0].CreatedAt)
	}
}

func TestProtectedDocumentRequiresAndForwardsVerifiedActor(t *testing.T) {
	documents := &documentServiceStub{document: completeDocument()}
	handler := newTestHandler(t, documents, tokenVerifierStub{})
	_, err := handler.GetDocument(context.Background(), &knowledgev1.DocumentIDRequest{DocumentId: documents.document.ID})
	assertBusinessCode(t, err, knowledgev1.CodeUnauthenticated)
	if documents.getCalls != 0 {
		t.Fatalf("Get() calls = %d", documents.getCalls)
	}

	handler = newTestHandler(t, documents, tokenVerifierStub{principal: coreauth.Principal{UserID: 42}})
	ctx := coreauth.WithAccessToken(context.Background(), "signed-token")
	response, err := handler.GetDocument(ctx, &knowledgev1.DocumentIDRequest{DocumentId: documents.document.ID})
	if err != nil || response == nil || documents.actorID != 42 {
		t.Fatalf("GetDocument() = %#v, %v, actor = %d", response, err, documents.actorID)
	}
}

func TestHandlerPreservesStableKnowledgeBusinessErrors(t *testing.T) {
	documents := &documentServiceStub{document: completeDocument(), err: knowledgeerrors.Precondition.Wrap(errors.New("private revision detail"))}
	handler := newTestHandler(t, documents, tokenVerifierStub{principal: coreauth.Principal{UserID: 42}})
	ctx := coreauth.WithAccessToken(context.Background(), "signed-token")
	_, err := handler.GetDocument(ctx, &knowledgev1.DocumentIDRequest{DocumentId: documents.document.ID})
	assertBusinessCode(t, err, knowledgev1.CodePreconditionFailed)
	businessError, _ := kerrors.FromBizStatusError(err)
	if businessError.BizMessage() != knowledgeerrors.Precondition.Message {
		t.Fatalf("business message = %q", businessError.BizMessage())
	}
}

func newTestHandler(t *testing.T, documents DocumentService, verifier TokenVerifier) *Handler {
	t.Helper()
	handler, err := NewHandler(
		documents, &memberServiceStub{}, &attachmentServiceStub{}, verifier, readinessStub{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertBusinessCode(t *testing.T, err error, code int32) {
	t.Helper()
	businessError, ok := kerrors.FromBizStatusError(err)
	if !ok || businessError.BizStatusCode() != code {
		t.Fatalf("business error = %#v, %v", businessError, err)
	}
}

func completeDocument() *domain.Document {
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	return &domain.Document{
		ID: "0198a3c0-0000-7000-8000-000000000001", Title: "Document", Slug: "document-0198a3c000",
		Owner: domain.PublicUser{ID: 42, Username: "alice"}, Access: domain.AccessOwner,
		MetadataRevision: 1, PermissionRevision: 1, CreatedAt: now, UpdatedAt: now,
	}
}

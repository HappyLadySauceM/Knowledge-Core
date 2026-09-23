package rpc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	knowledgelogic "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/logic"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

type documentServiceStub struct {
	page        knowledgelogic.DocumentPage
	document    *domain.Document
	err         error
	actorID     int64
	getCalls    int
	lastPublish knowledgelogic.PublishSnapshotInput
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
func (s *documentServiceStub) SetPublication(context.Context, string, int64, int64, bool, string) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) PublishSnapshot(_ context.Context, _ string, _, _ int64, input knowledgelogic.PublishSnapshotInput) (*domain.Document, error) {
	s.lastPublish = input
	return s.document, s.err
}
func (s *documentServiceStub) Delete(context.Context, string, int64, int64) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) Restore(context.Context, string, int64) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) PurgeDeleted(context.Context, string, int64, int64, string) error {
	return s.err
}
func (s *documentServiceStub) IsMediaPublished(context.Context, string) (bool, error) {
	return false, s.err
}
func (s *documentServiceStub) CreateCommit(context.Context, knowledgelogic.CommitInput) (*domain.Commit, error) {
	return nil, s.err
}
func (s *documentServiceStub) ListCommits(context.Context, string, int64, int) ([]*domain.Commit, error) {
	return nil, s.err
}
func (s *documentServiceStub) GetCommit(context.Context, string, int64) (*domain.Commit, error) {
	return nil, s.err
}
func (s *documentServiceStub) RenameCommit(context.Context, string, int64, string, string) (*domain.Commit, error) {
	return nil, s.err
}
func (s *documentServiceStub) RestoreCommit(context.Context, string, int64, string) (*domain.Document, error) {
	return s.document, s.err
}
func (s *documentServiceStub) ListHistory(context.Context, string, int64, int) ([]*repository.HistoryCheckpoint, error) {
	return nil, s.err
}
func (s *documentServiceStub) GetHistory(context.Context, string, string, int64) (*repository.HistoryCheckpoint, error) {
	return nil, s.err
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

type collaborationServiceStub struct {
	authorization *knowledgelogic.CollaborationAuthorization
	authorizeErr  error
	actorID       int64
	projected     domain.RichTextDocument
	sequence      int64
	plainText     string
	projectErr    error
}

func (s *collaborationServiceStub) Authorize(_ context.Context, _ string, actorID int64) (*knowledgelogic.CollaborationAuthorization, error) {
	s.actorID = actorID
	return s.authorization, s.authorizeErr
}

func (s *collaborationServiceStub) Project(_ context.Context, _ string, sequence int64, content domain.RichTextDocument, plainText string) error {
	s.sequence = sequence
	s.projected = content
	s.plainText = plainText
	return s.projectErr
}

type tokenVerifierStub struct {
	principal coreauth.Principal
	err       error
}

func (s tokenVerifierStub) Verify(string) (coreauth.Principal, error) { return s.principal, s.err }

type readinessStub struct {
	err   error
	calls *int
}

func (s readinessStub) Ready(context.Context) error {
	if s.calls != nil {
		(*s.calls)++
	}
	return s.err
}

func TestLiveIsIndependentFromReadiness(t *testing.T) {
	calls := 0
	handler := newTestHandlerWithReadiness(t, readinessStub{
		err:   errors.New("collaboration is not ready"),
		calls: &calls,
	})
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return now }

	if _, err := handler.Live(context.Background(), nil); err == nil {
		t.Fatal("Live() accepted a nil request")
	} else {
		assertBusinessCode(t, err, knowledgev1.CodeInvalidInput)
	}
	response, err := handler.Live(context.Background(), &commonv1.PingRequest{})
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if response == nil || response.Service != serviceName || response.Status != "live" || response.UnixTime != now.Unix() {
		t.Fatalf("Live() response = %#v", response)
	}
	if calls != 0 {
		t.Fatalf("readiness calls = %d, want 0", calls)
	}
}

func TestPingReportsDependencyReadiness(t *testing.T) {
	handler := newTestHandlerWithReadiness(t, readinessStub{})
	if _, err := handler.Ping(context.Background(), nil); err == nil {
		t.Fatal("Ping() accepted a nil request")
	} else {
		assertBusinessCode(t, err, knowledgev1.CodeInvalidInput)
	}

	tests := []struct {
		name   string
		err    error
		status string
	}{
		{name: "ready", status: "ready"},
		{name: "not ready", err: errors.New("collaboration is not ready"), status: "not_ready"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			handler := newTestHandlerWithReadiness(t, readinessStub{err: test.err, calls: &calls})
			response, err := handler.Ping(context.Background(), &commonv1.PingRequest{})
			if err != nil {
				t.Fatalf("Ping() error = %v", err)
			}
			if response == nil || response.Service != serviceName || response.Status != test.status {
				t.Fatalf("Ping() response = %#v", response)
			}
			if calls != 1 {
				t.Fatalf("readiness calls = %d, want 1", calls)
			}
		})
	}
}

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

	handler = newTestHandler(t, documents, tokenVerifierStub{principal: coreauth.Principal{UserID: 42, ExpiresAt: time.Now().Add(time.Minute)}})
	ctx := coreauth.WithAccessToken(context.Background(), "signed-token")
	response, err := handler.GetDocument(ctx, &knowledgev1.DocumentIDRequest{DocumentId: documents.document.ID})
	if err != nil || response == nil || documents.actorID != 42 {
		t.Fatalf("GetDocument() = %#v, %v, actor = %d", response, err, documents.actorID)
	}
}

func TestHandlerPreservesStableKnowledgeBusinessErrors(t *testing.T) {
	documents := &documentServiceStub{document: completeDocument(), err: knowledgeerrors.Precondition.Wrap(errors.New("private revision detail"))}
	handler := newTestHandler(t, documents, tokenVerifierStub{principal: coreauth.Principal{UserID: 42, ExpiresAt: time.Now().Add(time.Minute)}})
	ctx := coreauth.WithAccessToken(context.Background(), "signed-token")
	_, err := handler.GetDocument(ctx, &knowledgev1.DocumentIDRequest{DocumentId: documents.document.ID})
	assertBusinessCode(t, err, knowledgev1.CodePreconditionFailed)
	businessError, _ := kerrors.FromBizStatusError(err)
	if businessError.BizMessage() != knowledgeerrors.Precondition.Message {
		t.Fatalf("business message = %q", businessError.BizMessage())
	}
}

func TestAuthorizeCollaborationRequiresTokenAndReturnsActorExpiry(t *testing.T) {
	document := completeDocument()
	actor := domain.PublicUser{ID: 42, Username: "alice"}
	service := &collaborationServiceStub{authorization: &knowledgelogic.CollaborationAuthorization{Document: document, User: &actor}}
	expiresAt := time.Date(2026, time.August, 3, 12, 15, 0, 0, time.UTC)
	handler := newTestHandlerWithCollaboration(t, &documentServiceStub{}, service, tokenVerifierStub{principal: coreauth.Principal{UserID: 42, ExpiresAt: expiresAt}})
	request := &knowledgev1.AuthorizeCollaborationRequest{DocumentId: document.ID}
	if _, err := handler.AuthorizeCollaboration(context.Background(), request); err == nil {
		t.Fatal("AuthorizeCollaboration() accepted a missing token")
	}
	response, err := handler.AuthorizeCollaboration(coreauth.WithAccessToken(context.Background(), "signed-token"), request)
	if err != nil || response == nil || response.Actor == nil || response.Actor.Id != 42 || service.actorID != 42 ||
		response.TokenExpiresAt != expiresAt.Format(time.RFC3339Nano) || response.PermissionRevision != document.PermissionRevision {
		t.Fatalf("AuthorizeCollaboration() = %#v, %v, actor = %d", response, err, service.actorID)
	}
}

func TestProjectCollaborationValidatesAndConvertsRichText(t *testing.T) {
	service := &collaborationServiceStub{}
	handler := newTestHandlerWithCollaboration(t, &documentServiceStub{}, service, tokenVerifierStub{})
	if err := handler.ProjectCollaboration(context.Background(), &knowledgev1.ProjectCollaborationRequest{}); err == nil {
		t.Fatal("ProjectCollaboration() accepted nil content")
	}
	text := "hello"
	err := handler.ProjectCollaboration(context.Background(), &knowledgev1.ProjectCollaborationRequest{
		DocumentId: completeDocument().ID, Sequence: 7, PlainText: "hello",
		Content: &knowledgev1.RichTextDocument{Type: "doc", Content: []*knowledgev1.RichTextNode{{
			Type: "paragraph", Content: []*knowledgev1.RichTextNode{{Type: "text", Text: &text}},
		}}},
	})
	if err != nil || service.sequence != 7 || service.plainText != "hello" || len(service.projected.Content) != 1 {
		t.Fatalf("ProjectCollaboration() error = %v, service = %#v", err, service)
	}
	if err := service.projected.Validate(); err != nil {
		t.Fatalf("projected content.Validate() error = %v", err)
	}
	deep := &knowledgev1.RichTextNode{Type: "paragraph"}
	for range 65 {
		deep = &knowledgev1.RichTextNode{Type: "paragraph", Content: []*knowledgev1.RichTextNode{deep}}
	}
	err = handler.ProjectCollaboration(context.Background(), &knowledgev1.ProjectCollaborationRequest{
		DocumentId: completeDocument().ID, Content: &knowledgev1.RichTextDocument{Type: "doc", Content: []*knowledgev1.RichTextNode{deep}},
	})
	assertBusinessCode(t, err, knowledgev1.CodeInvalidInput)
}

func TestParagraphOnlyRichTextPassesPublishSnapshotAndProjectCollaboration(t *testing.T) {
	documents := &documentServiceStub{document: completeDocument()}
	collaboration := &collaborationServiceStub{}
	handler := newTestHandlerWithCollaboration(t, documents, collaboration, authenticatedVerifier())
	content := paragraphOnlyTransportDocument("hello")

	if err := handler.ProjectCollaboration(context.Background(), &knowledgev1.ProjectCollaborationRequest{
		DocumentId: completeDocument().ID, Sequence: 4, PlainText: "hello", Content: content,
	}); err != nil {
		t.Fatalf("ProjectCollaboration(paragraph-only) error = %v", err)
	}
	if err := collaboration.projected.Validate(); err != nil {
		t.Fatalf("ProjectCollaboration projected.Validate() error = %v", err)
	}
	if collaboration.projected.Content[0].Content[0].Content != nil {
		t.Fatalf("text node content = %#v, want nil", collaboration.projected.Content[0].Content[0].Content)
	}

	published, err := handler.PublishSnapshot(
		coreauth.WithAccessToken(context.Background(), "signed-token"),
		&knowledgev1.PublishSnapshotRequest{
			DocumentId: completeDocument().ID, ExpectedMetadataRevision: 1,
			Title: "Document",
			Slug:  "document", Language: "en", Tags: []string{}, Content: content, PlainText: "hello",
		},
	)
	if err != nil || published == nil {
		t.Fatalf("PublishSnapshot(paragraph-only) = %#v, %v", published, err)
	}
	if err := documents.lastPublish.Content.Validate(); err != nil {
		t.Fatalf("PublishSnapshot converted content.Validate() error = %v", err)
	}
}

func TestPublishSnapshotMapsRichTextParseFailureToInvalidInput(t *testing.T) {
	documents := &documentServiceStub{document: completeDocument()}
	handler := newTestHandler(t, documents, authenticatedVerifier())
	_, err := handler.PublishSnapshot(
		coreauth.WithAccessToken(context.Background(), "signed-token"),
		&knowledgev1.PublishSnapshotRequest{
			DocumentId: completeDocument().ID, ExpectedMetadataRevision: 1,
			Title: "Document", Slug: "document", Language: "en",
			Tags: []string{},
		},
	)
	assertBusinessCode(t, err, knowledgev1.CodeInvalidInput)
	assertBusinessMessage(t, err, "invalid knowledge input")
}

func TestKnowledgeValidationFailureLogsFieldAndReasonWithoutLeakingThem(t *testing.T) {
	capture := &captureHandler{}
	documents := &documentServiceStub{
		document: completeDocument(),
		err: knowledgeerrors.InvalidInput.Wrap(&domain.ValidationError{
			Field: "content", Reason: "contains an invalid text node",
		}),
	}
	handler := newTestHandlerWithLogger(t, documents, &collaborationServiceStub{}, authenticatedVerifier(), slog.New(capture))
	_, err := handler.PublishSnapshot(
		coreauth.WithAccessToken(context.Background(), "signed-token"),
		&knowledgev1.PublishSnapshotRequest{
			DocumentId: completeDocument().ID, ExpectedMetadataRevision: 1,
			Title: "Document", Slug: "document", Language: "en",
			Tags: []string{}, Content: paragraphOnlyTransportDocument("hello"), PlainText: "hello",
		},
	)
	assertBusinessCode(t, err, knowledgev1.CodeInvalidInput)
	assertBusinessMessage(t, err, "invalid knowledge input")
	if len(capture.records) != 1 {
		t.Fatalf("log records = %d, want 1", len(capture.records))
	}
	if got := slogAttr(capture.records[0], "validation.field"); got != "content" {
		t.Fatalf("validation.field = %q", got)
	}
	if got := slogAttr(capture.records[0], "validation.reason"); got != "contains an invalid text node" {
		t.Fatalf("validation.reason = %q", got)
	}
}

func newTestHandler(t *testing.T, documents DocumentService, verifier TokenVerifier) *Handler {
	return newTestHandlerWithCollaboration(t, documents, &collaborationServiceStub{}, verifier)
}

func newTestHandlerWithLogger(
	t *testing.T,
	documents DocumentService,
	collaboration CollaborationService,
	verifier TokenVerifier,
	logger *slog.Logger,
) *Handler {
	t.Helper()
	handler, err := NewHandler(
		documents, &memberServiceStub{}, collaboration, verifier, readinessStub{}, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newTestHandlerWithCollaboration(
	t *testing.T,
	documents DocumentService,
	collaboration CollaborationService,
	verifier TokenVerifier,
) *Handler {
	return newTestHandlerWithDependencies(t, documents, collaboration, verifier, readinessStub{})
}

func newTestHandlerWithReadiness(t *testing.T, readiness Readiness) *Handler {
	return newTestHandlerWithDependencies(
		t, &documentServiceStub{}, &collaborationServiceStub{}, tokenVerifierStub{}, readiness,
	)
}

func newTestHandlerWithDependencies(
	t *testing.T,
	documents DocumentService,
	collaboration CollaborationService,
	verifier TokenVerifier,
	readiness Readiness,
) *Handler {
	t.Helper()
	handler, err := NewHandler(
		documents, &memberServiceStub{}, collaboration, verifier, readiness,
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

func assertBusinessMessage(t *testing.T, err error, message string) {
	t.Helper()
	businessError, ok := kerrors.FromBizStatusError(err)
	if !ok || businessError.BizMessage() != message {
		t.Fatalf("business message = %#v, %v", businessError, err)
	}
}

func authenticatedVerifier() tokenVerifierStub {
	return tokenVerifierStub{principal: coreauth.Principal{
		UserID: 42, ExpiresAt: time.Date(2026, time.August, 3, 12, 15, 0, 0, time.UTC),
	}}
}

func paragraphOnlyTransportDocument(text string) *knowledgev1.RichTextDocument {
	return &knowledgev1.RichTextDocument{Type: "doc", Content: []*knowledgev1.RichTextNode{{
		Type: "paragraph", Content: []*knowledgev1.RichTextNode{{
			Type: "text", Text: &text, Content: []*knowledgev1.RichTextNode{},
		}},
	}}}
}

type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

func slogAttr(record slog.Record, key string) string {
	var value string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			value = attr.Value.String()
			return false
		}
		return true
	})
	return value
}

func completeDocument() *domain.Document {
	now := time.Date(2026, time.August, 2, 12, 0, 0, 0, time.UTC)
	return &domain.Document{
		ID: "0198a3c0-0000-7000-8000-000000000001", Title: "Document", Slug: "document-0198a3c000",
		Owner: domain.PublicUser{ID: 42, Username: "alice"}, Access: domain.AccessOwner,
		MetadataRevision: 1, PermissionRevision: 1, CreatedAt: now, UpdatedAt: now,
	}
}

package kitex_test

import (
	"context"
	"errors"
	"testing"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/health"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	knowledgeapp "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	knowledgekitex "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestPing(t *testing.T) {
	registry := health.NewRegistry()
	registry.SetServing(true)
	response, err := knowledgekitex.NewHandler(nil, nil, registry).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "knowledge" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

func TestPingReportsNotReadyWhileDraining(t *testing.T) {
	response, err := knowledgekitex.NewHandler(nil, nil, health.NewRegistry()).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Status != "not_ready" {
		t.Fatalf("Ping() status = %q", response.Status)
	}
}

func TestCreateDocumentRequiresVerifiedAdministrator(t *testing.T) {
	application := &fakeApplication{}
	handler := knowledgekitex.NewHandler(application, fakeVerifier{principal: auth.Principal{UserID: 7, Role: "admin", TokenVersion: 1}}, nil)
	ctx := auth.WithAccessToken(context.Background(), "access-token")
	response, err := handler.CreateDocument(ctx, &knowledgerpc.CreateDocumentRequest{Title: "First document"})
	if err != nil {
		t.Fatalf("CreateDocument() error = %v", err)
	}
	if response.Document == nil || response.Document.AuthorId != 7 || application.created.AuthorID != 7 {
		t.Fatalf("CreateDocument() = %#v, %#v", response, application.created)
	}

	denied := knowledgekitex.NewHandler(application, fakeVerifier{principal: auth.Principal{UserID: 7, Role: "user", TokenVersion: 1}}, nil)
	_, err = denied.CreateDocument(ctx, &knowledgerpc.CreateDocumentRequest{Title: "Denied"})
	assertRPCStatus(t, err, knowledgeerrors.Forbidden, nil)
}

func TestCreateDocumentPreservesVerifierCause(t *testing.T) {
	cause := errors.New("private token detail")
	handler := knowledgekitex.NewHandler(&fakeApplication{}, fakeVerifier{err: cause}, nil)
	ctx := auth.WithAccessToken(context.Background(), "invalid-access-token")

	_, err := handler.CreateDocument(ctx, &knowledgerpc.CreateDocumentRequest{Title: "Denied"})
	assertRPCStatus(t, err, knowledgeerrors.Forbidden, cause)
}

func TestGetPublishedDocumentMapsSafeApplicationErrors(t *testing.T) {
	validationCause := &domain.ValidationError{Field: "document_id", Reason: "private validation detail"}
	internalCause := errors.New("private database detail")
	tests := []struct {
		name    string
		cause   error
		mapping knowledgeerrors.Mapping
	}{
		{name: "validation", cause: validationCause, mapping: knowledgeerrors.InvalidInput},
		{name: "not found", cause: knowledgeapp.ErrDocumentNotFound, mapping: knowledgeerrors.NotFound},
		{name: "conflict", cause: knowledgeapp.ErrVersionConflict, mapping: knowledgeerrors.Conflict},
		{name: "internal", cause: internalCause, mapping: knowledgeerrors.Internal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := knowledgekitex.NewHandler(&fakeApplication{err: test.cause}, nil, nil)
			_, err := handler.GetPublishedDocument(context.Background(), &knowledgerpc.DocumentIDRequest{DocumentId: 7})
			assertRPCStatus(t, err, test.mapping, test.cause)
		})
	}
}

func TestListPublishedDocumentsRejectsNilRequest(t *testing.T) {
	_, err := knowledgekitex.NewHandler(&fakeApplication{}, nil, nil).ListPublishedDocuments(context.Background(), nil)
	assertRPCStatus(t, err, knowledgeerrors.InvalidInput, nil)
}

func assertRPCStatus(t *testing.T, err error, mapping knowledgeerrors.Mapping, cause error) {
	t.Helper()
	bizError, ok := kerrors.FromBizStatusError(err)
	definition := mapping.Definition()
	if !ok || bizError.BizStatusCode() != mapping.Code() || bizError.BizMessage() != definition.SafeMessage() {
		t.Fatalf("business error = %v, want code %d and message %q", err, mapping.Code(), definition.SafeMessage())
	}
	if key, kind := rpcerror.Metadata(err); key != definition.Key() || kind != definition.Kind() {
		t.Fatalf("business metadata = %q %q, want %q %q", key, kind, definition.Key(), definition.Kind())
	}
	if !errors.Is(err, definition) {
		t.Fatalf("business error does not match definition %q", definition.Key())
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("business error does not retain cause %v", cause)
	}
}

type fakeVerifier struct {
	principal auth.Principal
	err       error
}

func (f fakeVerifier) Verify(string) (auth.Principal, error) { return f.principal, f.err }

type fakeApplication struct {
	created knowledgeapp.CreateInput
	err     error
}

func (*fakeApplication) ListPublished(context.Context, knowledgeapp.ListInput) (domain.List, error) {
	return domain.List{}, nil
}

func (*fakeApplication) List(context.Context, knowledgeapp.ListInput) (domain.List, error) {
	return domain.List{}, nil
}

func (f *fakeApplication) GetPublished(context.Context, int64) (*domain.Detail, error) {
	return nil, f.err
}

func (f *fakeApplication) Create(_ context.Context, input knowledgeapp.CreateInput) (*domain.Detail, error) {
	f.created = input
	if f.err != nil {
		return nil, f.err
	}
	return &domain.Detail{Document: &domain.Document{ID: 1, Title: input.Title, Status: domain.StatusDraft, AuthorID: input.AuthorID}}, nil
}

func (*fakeApplication) Get(context.Context, int64) (*domain.Detail, error) { return nil, nil }

func (*fakeApplication) Update(context.Context, knowledgeapp.UpdateInput) (*domain.Detail, error) {
	return nil, nil
}

func (*fakeApplication) Delete(context.Context, int64) (*domain.Document, error) { return nil, nil }

func (*fakeApplication) SetStatus(context.Context, int64, string, int64) (*domain.Document, error) {
	return nil, nil
}

func (*fakeApplication) ApplyOperation(context.Context, domain.Operation) (domain.OperationAck, error) {
	return domain.OperationAck{}, nil
}

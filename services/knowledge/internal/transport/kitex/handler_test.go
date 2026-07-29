package kitex_test

import (
	"context"
	"testing"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	knowledgeapp "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgekitex "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/transport/kitex"
	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestPing(t *testing.T) {
	response, err := knowledgekitex.NewHandler(nil, nil).Ping(context.Background(), &common.PingRequest{})
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if response.Service != "knowledge" || response.Status != "ok" || response.UnixTime == 0 {
		t.Fatalf("Ping() = %#v", response)
	}
}

func TestCreateDocumentRequiresVerifiedAdministrator(t *testing.T) {
	application := &fakeApplication{}
	handler := knowledgekitex.NewHandler(application, fakeVerifier{principal: auth.Principal{UserID: 7, Role: "admin", TokenVersion: 1}})
	ctx := auth.WithAccessToken(context.Background(), "access-token")
	response, err := handler.CreateDocument(ctx, &knowledgerpc.CreateDocumentRequest{Title: "First document"})
	if err != nil {
		t.Fatalf("CreateDocument() error = %v", err)
	}
	if response.Document == nil || response.Document.AuthorId != 7 || application.created.AuthorID != 7 {
		t.Fatalf("CreateDocument() = %#v, %#v", response, application.created)
	}

	denied := knowledgekitex.NewHandler(application, fakeVerifier{principal: auth.Principal{UserID: 7, Role: "user", TokenVersion: 1}})
	_, err = denied.CreateDocument(ctx, &knowledgerpc.CreateDocumentRequest{Title: "Denied"})
	bizError, ok := kerrors.FromBizStatusError(err)
	if !ok || bizError.BizStatusCode() != knowledgerpc.CodeForbidden {
		t.Fatalf("CreateDocument() denial error = %v", err)
	}
}

type fakeVerifier struct {
	principal auth.Principal
	err       error
}

func (f fakeVerifier) Verify(string) (auth.Principal, error) { return f.principal, f.err }

type fakeApplication struct {
	created knowledgeapp.CreateInput
}

func (*fakeApplication) ListPublished(context.Context, knowledgeapp.ListInput) (domain.List, error) {
	return domain.List{}, nil
}

func (*fakeApplication) List(context.Context, knowledgeapp.ListInput) (domain.List, error) {
	return domain.List{}, nil
}

func (*fakeApplication) GetPublished(context.Context, int64) (*domain.Detail, error) { return nil, nil }

func (f *fakeApplication) Create(_ context.Context, input knowledgeapp.CreateInput) (*domain.Detail, error) {
	f.created = input
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

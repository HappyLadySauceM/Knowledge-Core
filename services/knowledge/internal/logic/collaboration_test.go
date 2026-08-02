package logic

import (
	"context"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
)

type collaborationRepositoryStub struct {
	document   *domain.Document
	projected  domain.Projection
	authorized int
}

func (s *collaborationRepositoryStub) AuthorizeCollaboration(context.Context, string, int64) (*domain.Document, error) {
	s.authorized++
	return s.document, nil
}

func (s *collaborationRepositoryStub) UpsertProjection(_ context.Context, value domain.Projection) error {
	s.projected = value
	return nil
}

type directoryStub struct {
	current domain.PublicUser
	calls   int
}

func (s *directoryStub) CurrentUser(context.Context) (domain.PublicUser, error) {
	s.calls++
	return s.current, nil
}

func (s *directoryStub) ResolveUser(context.Context, string) (domain.PublicUser, error) {
	return domain.PublicUser{}, nil
}

func TestCollaborationAuthorizeSupportsAnonymousAndChecksAuthenticatedIdentity(t *testing.T) {
	repository := &collaborationRepositoryStub{document: &domain.Document{ID: "0198a3c0-0000-7000-8000-000000000001", Access: domain.AccessViewer}}
	directory := &directoryStub{current: domain.PublicUser{ID: 42, Username: "alice"}}
	logic, err := NewCollaborationLogic(repository, directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logic.Authorize(context.Background(), repository.document.ID, 0); err != nil {
		t.Fatalf("Authorize(anonymous) error = %v", err)
	}
	if directory.calls != 0 {
		t.Fatalf("anonymous directory calls = %d", directory.calls)
	}
	authorization, err := logic.Authorize(context.Background(), repository.document.ID, 42)
	if err != nil {
		t.Fatalf("Authorize(authenticated) error = %v", err)
	}
	if directory.calls != 1 || authorization.User == nil || authorization.User.ID != 42 {
		t.Fatalf("authorization = %#v, directory calls = %d", authorization, directory.calls)
	}
}

func TestCollaborationProjectValidatesAndForwardsProjection(t *testing.T) {
	repository := &collaborationRepositoryStub{}
	logic, _ := NewCollaborationLogic(repository, &directoryStub{})
	documentID := "0198a3c0-0000-7000-8000-000000000001"
	content := domain.RichTextDocument{Type: "doc", Content: []*domain.RichTextNode{{Type: "paragraph"}}}
	if err := logic.Project(context.Background(), documentID, 7, content, "  hello  "); err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if repository.projected.Sequence != 7 || repository.projected.PlainText != "hello" || len(repository.projected.Content) == 0 {
		t.Fatalf("projection = %#v", repository.projected)
	}
	if err := logic.Project(context.Background(), documentID, -1, content, ""); err == nil {
		t.Fatal("Project() accepted a negative sequence")
	}
}

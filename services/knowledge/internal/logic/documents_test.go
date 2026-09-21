package logic

import (
	"context"
	"errors"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

var errUnusedDocumentRepository = errors.New("unused document repository method")

type documentRepositoryStub struct {
	snapshot repository.PublicationSnapshotInput
	document *domain.Document
}

func (s *documentRepositoryStub) CreateDocument(context.Context, *domain.Document, repository.Idempotency) error {
	return errUnusedDocumentRepository
}
func (s *documentRepositoryStub) GetDocument(context.Context, string, int64, bool) (*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) GetPublishedDocument(context.Context, string, int64) (*domain.Document, *domain.Projection, bool, error) {
	return nil, nil, false, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) ListDocuments(context.Context, repository.ListOptions) ([]*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) UpdateDocument(context.Context, string, int64, int64, *string, *string, *string, *string, []string, *string, *string, *string, *float64, *float64) (*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) SetPublication(context.Context, string, int64, int64, bool, repository.Idempotency) (*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) PublishSnapshot(_ context.Context, _ string, _, _ int64, input repository.PublicationSnapshotInput) (*domain.Document, error) {
	s.snapshot = input
	return s.document, nil
}
func (s *documentRepositoryStub) SoftDeleteDocument(context.Context, string, int64, int64) (*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) RestoreDeletedDocument(context.Context, string, int64) (*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) PurgeDeletedDocument(context.Context, string, int64, int64, repository.Idempotency) error {
	return errUnusedDocumentRepository
}
func (s *documentRepositoryStub) IsMediaPublished(context.Context, string) (bool, error) {
	return false, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) ListFolders(context.Context, int64, *string) ([]*domain.Folder, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) CreateFolder(context.Context, int64, string, *string, repository.Idempotency) (*domain.Folder, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) UpdateFolder(context.Context, int64, string, int64, *string, *string) (*domain.Folder, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) DeleteFolder(context.Context, int64, string, int64) error {
	return errUnusedDocumentRepository
}
func (s *documentRepositoryStub) CreateCommit(context.Context, string, int64, repository.CommitInput) (*domain.Commit, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) ListCommits(context.Context, string, int64, int) ([]*domain.Commit, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) GetCommit(context.Context, string, int64) (*domain.Commit, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) RenameCommit(context.Context, string, int64, string, string) (*domain.Commit, error) {
	return nil, errUnusedDocumentRepository
}
func (s *documentRepositoryStub) RestoreCommit(context.Context, string, int64, repository.Idempotency) (*domain.Document, error) {
	return nil, errUnusedDocumentRepository
}

func TestPublishSnapshotAcceptsParagraphOnlyTextWithEmptyContent(t *testing.T) {
	document := &domain.Document{
		ID: "0198a3c0-0000-7000-8000-000000000001", Title: "Document", Slug: "document-body",
		Language: "en", Access: domain.AccessOwner, MetadataRevision: 1,
	}
	repositoryStub := &documentRepositoryStub{document: document}
	logic, err := NewDocumentLogic(repositoryStub, &directoryStub{current: domain.PublicUser{ID: 42, Username: "alice"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := logic.PublishSnapshot(context.Background(), document.ID, 42, 1, PublishSnapshotInput{
		Title: "Document", Slug: "document-body", Language: "en",
		Content: paragraphOnlyDocument("hello"), PlainText: "hello",
	})
	if err != nil {
		t.Fatalf("PublishSnapshot(paragraph-only) error = %v", err)
	}
	if result == nil || repositoryStub.snapshot.PlainText != "hello" {
		t.Fatalf("PublishSnapshot() = %#v, snapshot = %#v", result, repositoryStub.snapshot)
	}
}

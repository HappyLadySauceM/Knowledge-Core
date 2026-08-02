package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

type DocumentRepository interface {
	CreateDocument(context.Context, *domain.Document, repository.Idempotency) error
	GetDocument(context.Context, string, int64, bool) (*domain.Document, error)
	GetPublishedDocument(context.Context, string, int64) (*domain.Document, *domain.Projection, bool, error)
	ListDocuments(context.Context, repository.ListOptions) ([]*domain.Document, error)
	UpdateDocument(context.Context, string, int64, int64, *string, *string, *string) (*domain.Document, error)
	SetPublication(context.Context, string, int64, int64, bool) (*domain.Document, error)
	SoftDeleteDocument(context.Context, string, int64, int64) (*domain.Document, error)
	RestoreDeletedDocument(context.Context, string, int64) (*domain.Document, error)
	ListReadyAttachments(context.Context, string) ([]*domain.Attachment, error)
}

type DocumentLogic struct {
	repository DocumentRepository
	directory  Directory
	now        func() time.Time
}

type ListDocumentsInput struct {
	ActorID     int64
	Query       string
	Cursor      string
	Limit       int32
	Access      string
	Publication string
}

type DocumentPage struct {
	Items      []*domain.Document
	NextCursor *string
	HasMore    bool
}

type DocumentDetail struct {
	Document    *domain.Document
	Content     domain.RichTextDocument
	PlainText   string
	Attachments []*domain.Attachment
	Redirect    bool
}

type CreateDocumentInput struct {
	Title          string
	Summary        *string
	Slug           *string
	IdempotencyKey string
}

type UpdateDocumentInput struct {
	DocumentID       string
	ActorID          int64
	ExpectedRevision int64
	Title            *string
	Summary          *string
	Slug             *string
}

func NewDocumentLogic(repository DocumentRepository, directory Directory) (*DocumentLogic, error) {
	if repository == nil || directory == nil {
		return nil, errors.New("create document logic: repository and directory are required")
	}
	return &DocumentLogic{repository: repository, directory: directory, now: time.Now}, nil
}

func (l *DocumentLogic) ListPublished(ctx context.Context, input ListDocumentsInput) (DocumentPage, error) {
	if err := validateListInput(input, true); err != nil {
		return DocumentPage{}, mapError(err)
	}
	cursor, err := repository.DecodeCursor(input.Cursor)
	if err != nil {
		return DocumentPage{}, mapError(&domain.ValidationError{Field: "cursor", Reason: "is invalid"})
	}
	limit := effectiveLimit(input.Limit)
	documents, err := l.repository.ListDocuments(ctx, repository.ListOptions{
		ActorID: input.ActorID, Query: strings.TrimSpace(input.Query), Cursor: cursor, Limit: limit, Published: true,
	})
	if err != nil {
		return DocumentPage{}, mapError(err)
	}
	return buildDocumentPage(documents, limit, func(document *domain.Document) time.Time {
		if document.PublishedAt != nil {
			return *document.PublishedAt
		}
		return document.UpdatedAt
	})
}

func (l *DocumentLogic) GetPublished(ctx context.Context, slug string, actorID int64) (*DocumentDetail, error) {
	requested := strings.TrimSpace(slug)
	normalized, err := domain.NormalizeSlug(requested)
	if err != nil {
		return nil, mapError(err)
	}
	document, projection, redirect, err := l.repository.GetPublishedDocument(ctx, normalized, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	content, err := parseRichText(projection.Content)
	if err != nil {
		return nil, mapError(err)
	}
	attachments, err := l.repository.ListReadyAttachments(ctx, document.ID)
	if err != nil {
		return nil, mapError(err)
	}
	return &DocumentDetail{
		Document: document, Content: content, PlainText: projection.PlainText,
		Attachments: attachments, Redirect: redirect || requested != normalized,
	}, nil
}

func (l *DocumentLogic) List(ctx context.Context, input ListDocumentsInput) (DocumentPage, error) {
	if input.ActorID <= 0 {
		return DocumentPage{}, mapError(repository.ErrForbidden)
	}
	if err := validateListInput(input, false); err != nil {
		return DocumentPage{}, mapError(err)
	}
	cursor, err := repository.DecodeCursor(input.Cursor)
	if err != nil {
		return DocumentPage{}, mapError(&domain.ValidationError{Field: "cursor", Reason: "is invalid"})
	}
	limit := effectiveLimit(input.Limit)
	documents, err := l.repository.ListDocuments(ctx, repository.ListOptions{
		ActorID: input.ActorID, Query: strings.TrimSpace(input.Query), Cursor: cursor, Limit: limit,
		Access: input.Access, Publication: input.Publication,
	})
	if err != nil {
		return DocumentPage{}, mapError(err)
	}
	return buildDocumentPage(documents, limit, func(document *domain.Document) time.Time { return document.UpdatedAt })
}

func (l *DocumentLogic) ListDeleted(ctx context.Context, input ListDocumentsInput) (DocumentPage, error) {
	if input.ActorID <= 0 {
		return DocumentPage{}, mapError(repository.ErrForbidden)
	}
	if err := validateListInput(input, false); err != nil {
		return DocumentPage{}, mapError(err)
	}
	cursor, err := repository.DecodeCursor(input.Cursor)
	if err != nil {
		return DocumentPage{}, mapError(&domain.ValidationError{Field: "cursor", Reason: "is invalid"})
	}
	limit := effectiveLimit(input.Limit)
	documents, err := l.repository.ListDocuments(ctx, repository.ListOptions{
		ActorID: input.ActorID, Query: strings.TrimSpace(input.Query), Cursor: cursor, Limit: limit, Deleted: true,
	})
	if err != nil {
		return DocumentPage{}, mapError(err)
	}
	return buildDocumentPage(documents, limit, func(document *domain.Document) time.Time {
		if document.DeletedAt != nil {
			return *document.DeletedAt
		}
		return document.UpdatedAt
	})
}

func (l *DocumentLogic) Create(ctx context.Context, input CreateDocumentInput) (*domain.Document, error) {
	input.Title = strings.TrimSpace(input.Title)
	if err := domain.ValidateTitle(input.Title); err != nil {
		return nil, mapError(err)
	}
	summary := ""
	if input.Summary != nil {
		summary = strings.TrimSpace(*input.Summary)
	}
	if err := domain.ValidateSummary(summary); err != nil {
		return nil, mapError(err)
	}
	owner, err := l.directory.CurrentUser(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	id, err := domain.NewID()
	if err != nil {
		return nil, mapError(err)
	}
	slug := domain.SlugFromTitle(input.Title, id)
	if input.Slug != nil {
		slug, err = domain.NormalizeSlug(*input.Slug)
		if err != nil {
			return nil, mapError(err)
		}
	}
	idempotencyValue, err := idempotency(owner.ID, "create_document", input.IdempotencyKey, struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Slug    string `json:"slug"`
	}{input.Title, summary, slug})
	if err != nil {
		return nil, mapError(err)
	}
	now := l.now().UTC()
	document := &domain.Document{
		ID: id, Title: input.Title, Summary: summary, Slug: slug, Owner: owner,
		Access: domain.AccessOwner, MetadataRevision: 1, PermissionRevision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := l.repository.CreateDocument(ctx, document, idempotencyValue); err != nil {
		return nil, mapError(err)
	}
	return document, nil
}

func (l *DocumentLogic) Get(ctx context.Context, documentID string, actorID int64) (*domain.Document, error) {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return nil, mapError(err)
	}
	document, err := l.repository.GetDocument(ctx, documentID, actorID, false)
	if err != nil {
		return nil, mapError(err)
	}
	if !domain.CanRead(document.Access) {
		return nil, mapError(repository.ErrForbidden)
	}
	return document, nil
}

func (l *DocumentLogic) Update(ctx context.Context, input UpdateDocumentInput) (*domain.Document, error) {
	if err := domain.ValidateID("document_id", input.DocumentID); err != nil {
		return nil, mapError(err)
	}
	if input.ExpectedRevision <= 0 {
		return nil, mapError(&domain.ValidationError{Field: "expected_revision", Reason: "must be positive"})
	}
	if input.Title == nil && input.Summary == nil && input.Slug == nil {
		return nil, mapError(&domain.ValidationError{Field: "document", Reason: "at least one field must be provided"})
	}
	if input.Title != nil {
		value := strings.TrimSpace(*input.Title)
		if err := domain.ValidateTitle(value); err != nil {
			return nil, mapError(err)
		}
		input.Title = &value
	}
	if input.Summary != nil {
		value := strings.TrimSpace(*input.Summary)
		if err := domain.ValidateSummary(value); err != nil {
			return nil, mapError(err)
		}
		input.Summary = &value
	}
	if input.Slug != nil {
		value, err := domain.NormalizeSlug(*input.Slug)
		if err != nil {
			return nil, mapError(err)
		}
		input.Slug = &value
	}
	result, err := l.repository.UpdateDocument(
		ctx, input.DocumentID, input.ActorID, input.ExpectedRevision, input.Title, input.Summary, input.Slug,
	)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) SetPublication(ctx context.Context, documentID string, actorID, expected int64, published bool) (*domain.Document, error) {
	if err := validateMutation(documentID, expected); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.SetPublication(ctx, documentID, actorID, expected, published)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) Delete(ctx context.Context, documentID string, actorID, expected int64) (*domain.Document, error) {
	if err := validateMutation(documentID, expected); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.SoftDeleteDocument(ctx, documentID, actorID, expected)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) Restore(ctx context.Context, documentID string, actorID int64) (*domain.Document, error) {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.RestoreDeletedDocument(ctx, documentID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func validateListInput(input ListDocumentsInput, public bool) error {
	if err := domain.ValidatePage(input.Limit, input.Cursor, input.Query); err != nil {
		return err
	}
	if public && (input.Access != "" || input.Publication != "") {
		return &domain.ValidationError{Field: "filters", Reason: "access and publication are not valid for the public collection"}
	}
	if input.Access != "" && input.Access != domain.AccessOwner && input.Access != "shared" {
		return &domain.ValidationError{Field: "access", Reason: "must be owner or shared"}
	}
	if input.Publication != "" && input.Publication != "published" && input.Publication != "draft" {
		return &domain.ValidationError{Field: "publication", Reason: "must be published or draft"}
	}
	return nil
}

func validateMutation(documentID string, expected int64) error {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return err
	}
	if expected <= 0 {
		return &domain.ValidationError{Field: "expected_revision", Reason: "must be positive"}
	}
	return nil
}

func buildDocumentPage(documents []*domain.Document, limit int, orderTime func(*domain.Document) time.Time) (DocumentPage, error) {
	hasMore := len(documents) > limit
	if hasMore {
		documents = documents[:limit]
	}
	var nextCursor *string
	if hasMore && len(documents) > 0 {
		last := documents[len(documents)-1]
		encoded, err := repository.EncodeCursor(repository.Cursor{Time: orderTime(last), ID: last.ID})
		if err != nil {
			return DocumentPage{}, fmt.Errorf("build document page: %w", err)
		}
		nextCursor = &encoded
	}
	return DocumentPage{Items: documents, NextCursor: nextCursor, HasMore: hasMore}, nil
}

func parseRichText(content []byte) (domain.RichTextDocument, error) {
	var document domain.RichTextDocument
	if err := jsoncodec.Unmarshal(content, &document); err != nil {
		return domain.RichTextDocument{}, fmt.Errorf("decode document projection: %w", err)
	}
	if err := document.Validate(); err != nil {
		return domain.RichTextDocument{}, err
	}
	return document, nil
}

package logic

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	UpdateDocument(context.Context, string, int64, int64, *string, *string, *string, *string, []string, *string, *string, *string, *float64, *float64) (*domain.Document, error)
	SetPublication(context.Context, string, int64, int64, bool, repository.Idempotency) (*domain.Document, error)
	PublishSnapshot(context.Context, string, int64, int64, repository.PublicationSnapshotInput) (*domain.Document, error)
	SoftDeleteDocument(context.Context, string, int64, int64) (*domain.Document, error)
	RestoreDeletedDocument(context.Context, string, int64) (*domain.Document, error)
	PurgeDeletedDocument(context.Context, string, int64, int64, repository.Idempotency) error
	IsMediaPublished(context.Context, string) (bool, error)
	ListFolders(context.Context, int64, *string) ([]*domain.Folder, error)
	CreateFolder(context.Context, int64, string, *string, repository.Idempotency) (*domain.Folder, error)
	UpdateFolder(context.Context, int64, string, int64, *string, *string) (*domain.Folder, error)
	DeleteFolder(context.Context, int64, string, int64) error
	CreateCommit(context.Context, string, int64, repository.CommitInput) (*domain.Commit, error)
	ListCommits(context.Context, string, int64, int) ([]*domain.Commit, error)
	GetCommit(context.Context, string, int64) (*domain.Commit, error)
	RenameCommit(context.Context, string, int64, string, string) (*domain.Commit, error)
	RestoreCommit(context.Context, string, int64, repository.Idempotency) (*domain.Document, error)
}

func (l *DocumentLogic) ListFolders(ctx context.Context, actorID int64, parentID *string) ([]*domain.Folder, error) {
	if actorID <= 0 {
		return nil, mapError(repository.ErrForbidden)
	}
	if parentID != nil && *parentID != "" {
		if err := domain.ValidateID("parent_id", *parentID); err != nil {
			return nil, mapError(err)
		}
	} else {
		parentID = nil
	}
	result, err := l.repository.ListFolders(ctx, actorID, parentID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) CreateFolder(ctx context.Context, actorID int64, name string, parentID *string, key string) (*domain.Folder, error) {
	if actorID <= 0 {
		return nil, mapError(repository.ErrForbidden)
	}
	name = strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	if len([]rune(name)) < 1 || len([]rune(name)) > 120 {
		return nil, mapError(&domain.ValidationError{Field: "name", Reason: "must contain 1-120 characters"})
	}
	if parentID != nil && *parentID != "" {
		if err := domain.ValidateID("parent_id", *parentID); err != nil {
			return nil, mapError(err)
		}
	} else {
		parentID = nil
	}
	idempotencyValue, err := idempotency(actorID, "create_folder", key, struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parent_id"`
	}{name, parentID})
	if err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.CreateFolder(ctx, actorID, name, parentID, idempotencyValue)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) UpdateFolder(ctx context.Context, actorID int64, id string, expected int64, name, parentID *string) (*domain.Folder, error) {
	if actorID <= 0 {
		return nil, mapError(repository.ErrForbidden)
	}
	if err := domain.ValidateID("folder_id", id); err != nil {
		return nil, mapError(err)
	}
	if expected <= 0 {
		return nil, mapError(&domain.ValidationError{Field: "expected_revision", Reason: "must be positive"})
	}
	if name != nil {
		value := strings.Join(strings.Fields(strings.TrimSpace(*name)), " ")
		if len([]rune(value)) < 1 || len([]rune(value)) > 120 {
			return nil, mapError(&domain.ValidationError{Field: "name", Reason: "must contain 1-120 characters"})
		}
		name = &value
	}
	if parentID != nil && *parentID != "" {
		if err := domain.ValidateID("parent_id", *parentID); err != nil {
			return nil, mapError(err)
		}
	} else if parentID != nil {
		parentID = new(string)
	}
	if name == nil && parentID == nil {
		return nil, mapError(&domain.ValidationError{Field: "folder", Reason: "at least one field must be provided"})
	}
	result, err := l.repository.UpdateFolder(ctx, actorID, id, expected, name, parentID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) DeleteFolder(ctx context.Context, actorID int64, id string, expected int64) error {
	if actorID <= 0 {
		return mapError(repository.ErrForbidden)
	}
	if err := domain.ValidateID("folder_id", id); err != nil {
		return mapError(err)
	}
	if expected <= 0 {
		return mapError(&domain.ValidationError{Field: "expected_revision", Reason: "must be positive"})
	}
	return mapError(l.repository.DeleteFolder(ctx, actorID, id, expected))
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
	FolderID    string
}

type DocumentPage struct {
	Items      []*domain.Document
	NextCursor *string
	HasMore    bool
}

type DocumentDetail struct {
	Document  *domain.Document
	Content   domain.RichTextDocument
	PlainText string
	Redirect  bool
}

type CreateDocumentInput struct {
	Title          string
	Summary        *string
	Slug           *string
	IdempotencyKey string
}

type UpdateDocumentInput struct {
	DocumentID        string
	ActorID           int64
	ExpectedRevision  int64
	Title             *string
	Summary           *string
	Slug              *string
	Language          *string
	Tags              []string
	FolderID          *string
	Icon              *string
	CoverAttachmentID *string
	CoverFocalX       *float64
	CoverFocalY       *float64
}

type PublishSnapshotInput struct {
	Title             string
	Summary           string
	Slug              string
	Language          string
	Tags              []string
	Content           domain.RichTextDocument
	PlainText         string
	PublicationHash   string
	Icon              string
	CoverAttachmentID *string
	CoverFocalX       float64
	CoverFocalY       float64
	IdempotencyKey    string
}

type CommitInput struct {
	DocumentID     string
	ActorID        int64
	Kind           string
	Label          string
	Description    string
	Content        *domain.RichTextDocument
	PlainText      string
	ContentHash    string
	IdempotencyKey string
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
	return &DocumentDetail{
		Document: document, Content: content, PlainText: projection.PlainText,
		Redirect: redirect || requested != normalized,
	}, nil
}

func (l *DocumentLogic) IsMediaPublished(ctx context.Context, attachmentID string) (bool, error) {
	if err := domain.ValidateID("attachment_id", attachmentID); err != nil {
		return false, mapError(err)
	}
	published, err := l.repository.IsMediaPublished(ctx, attachmentID)
	if err != nil {
		return false, mapError(err)
	}
	return published, nil
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
		FolderID: input.FolderID,
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
		ID: id, Title: input.Title, Summary: summary, Slug: slug, Language: "zh-CN", Owner: owner,
		Access: domain.AccessOwner, PublicationStatus: domain.PublicationDraft, MetadataRevision: 1, PermissionRevision: 1,
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
	if input.Language != nil {
		value := strings.TrimSpace(*input.Language)
		if len(value) < 2 || len(value) > 16 {
			return nil, mapError(&domain.ValidationError{Field: "language", Reason: "must contain 2-16 characters"})
		}
		input.Language = &value
	}
	if input.Tags != nil {
		tags, err := domain.NormalizeTags(input.Tags)
		if err != nil {
			return nil, mapError(err)
		}
		input.Tags = tags
	}
	if input.Icon != nil {
		value := strings.TrimSpace(*input.Icon)
		if len([]rune(value)) > 8 {
			return nil, mapError(&domain.ValidationError{Field: "icon", Reason: "must contain at most 8 characters"})
		}
		input.Icon = &value
	}
	if input.CoverAttachmentID != nil && *input.CoverAttachmentID != "" {
		if err := domain.ValidateID("cover_attachment_id", *input.CoverAttachmentID); err != nil {
			return nil, mapError(err)
		}
	}
	for field, value := range map[string]*float64{"cover_focal_x": input.CoverFocalX, "cover_focal_y": input.CoverFocalY} {
		if value != nil && (*value < 0 || *value > 100) {
			return nil, mapError(&domain.ValidationError{Field: field, Reason: "must be between 0 and 100"})
		}
	}
	if input.Title == nil && input.Summary == nil && input.Slug == nil && input.Language == nil && input.Tags == nil && input.FolderID == nil && input.Icon == nil && input.CoverAttachmentID == nil && input.CoverFocalX == nil && input.CoverFocalY == nil {
		return nil, mapError(&domain.ValidationError{Field: "document", Reason: "at least one field must be provided"})
	}
	result, err := l.repository.UpdateDocument(
		ctx, input.DocumentID, input.ActorID, input.ExpectedRevision, input.Title, input.Summary, input.Slug, input.Language, input.Tags, input.FolderID, input.Icon, input.CoverAttachmentID, input.CoverFocalX, input.CoverFocalY,
	)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) SetPublication(ctx context.Context, documentID string, actorID, expected int64, published bool, key string) (*domain.Document, error) {
	if err := validateMutation(documentID, expected); err != nil {
		return nil, mapError(err)
	}
	idempotencyValue, err := idempotency(actorID, "set_publication", key, struct {
		DocumentID       string `json:"document_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		Published        bool   `json:"published"`
	}{documentID, expected, published})
	if err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.SetPublication(ctx, documentID, actorID, expected, published, idempotencyValue)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) PublishSnapshot(ctx context.Context, documentID string, actorID, expected int64, input PublishSnapshotInput) (*domain.Document, error) {
	if err := validateMutation(documentID, expected); err != nil {
		return nil, mapError(err)
	}
	input.Title = strings.TrimSpace(input.Title)
	if err := domain.ValidateTitle(input.Title); err != nil {
		return nil, mapError(err)
	}
	input.Summary = strings.TrimSpace(input.Summary)
	if err := domain.ValidateSummary(input.Summary); err != nil {
		return nil, mapError(err)
	}
	normalizedSlug, err := domain.NormalizeSlug(input.Slug)
	if err != nil {
		return nil, mapError(err)
	}
	input.Slug = normalizedSlug
	input.Language = strings.TrimSpace(input.Language)
	if len(input.Language) < 2 || len(input.Language) > 16 {
		return nil, mapError(&domain.ValidationError{Field: "language", Reason: "must contain 2-16 characters"})
	}
	if err := input.Content.Validate(); err != nil {
		return nil, mapError(err)
	}
	if input.Tags == nil {
		input.Tags = []string{}
	} else {
		tags, tagErr := domain.NormalizeTags(input.Tags)
		if tagErr != nil {
			return nil, mapError(tagErr)
		}
		input.Tags = tags
	}
	if len(input.PlainText) > domain.MaxProjectionBytes {
		return nil, mapError(&domain.ValidationError{Field: "plain_text", Reason: "is too large"})
	}
	owner, err := l.directory.CurrentUser(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	idempotencyValue, err := idempotency(owner.ID, "publish_snapshot", input.IdempotencyKey, struct {
		DocumentID       string                  `json:"document_id"`
		ExpectedRevision int64                   `json:"expected_revision"`
		Title            string                  `json:"title"`
		Summary          string                  `json:"summary"`
		Slug             string                  `json:"slug"`
		Language         string                  `json:"language"`
		Tags             []string                `json:"tags"`
		Content          domain.RichTextDocument `json:"content"`
		PlainText        string                  `json:"plain_text"`
	}{documentID, expected, input.Title, input.Summary, input.Slug, input.Language, input.Tags, input.Content, input.PlainText})
	if err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.PublishSnapshot(ctx, documentID, actorID, expected, repository.PublicationSnapshotInput{
		Title: input.Title, Summary: input.Summary,
		Slug: input.Slug, Language: input.Language, Tags: append([]string(nil), input.Tags...), Content: input.Content,
		PlainText: input.PlainText, MediaIDs: publicationMediaIDs(input.Content), Idempotency: idempotencyValue,
		PublicationHash: input.PublicationHash, Icon: input.Icon, CoverAttachmentID: input.CoverAttachmentID,
		CoverFocalX: input.CoverFocalX, CoverFocalY: input.CoverFocalY,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) CreateCommit(ctx context.Context, input CommitInput) (*domain.Commit, error) {
	if err := domain.ValidateID("document_id", input.DocumentID); err != nil {
		return nil, mapError(err)
	}
	if input.ActorID <= 0 {
		return nil, mapError(repository.ErrForbidden)
	}
	if strings.TrimSpace(input.Kind) == "" {
		input.Kind = "manual"
	}
	if strings.TrimSpace(input.Label) == "" {
		input.Label = input.Kind
	}
	if len([]rune(input.Label)) > 160 || len([]rune(input.Description)) > 2000 {
		return nil, mapError(&domain.ValidationError{Field: "commit", Reason: "label and description are invalid"})
	}
	value, err := idempotency(input.ActorID, "create_commit", input.IdempotencyKey, struct {
		DocumentID  string `json:"document_id"`
		Kind        string `json:"kind"`
		Label       string `json:"label"`
		ContentHash string `json:"content_hash"`
	}{input.DocumentID, input.Kind, input.Label, input.ContentHash})
	if err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.CreateCommit(ctx, input.DocumentID, input.ActorID, repository.CommitInput{
		Kind: input.Kind, Label: input.Label, Description: input.Description, Content: input.Content,
		PlainText: input.PlainText, ContentHash: input.ContentHash, Idempotency: value,
	})
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) ListCommits(ctx context.Context, documentID string, actorID int64, limit int) ([]*domain.Commit, error) {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return nil, mapError(err)
	}
	if actorID <= 0 {
		return nil, mapError(repository.ErrForbidden)
	}
	result, err := l.repository.ListCommits(ctx, documentID, actorID, limit)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) GetCommit(ctx context.Context, commitID string, actorID int64) (*domain.Commit, error) {
	if err := domain.ValidateID("commit_id", commitID); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.GetCommit(ctx, commitID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) RenameCommit(ctx context.Context, commitID string, actorID int64, label, description string) (*domain.Commit, error) {
	if err := domain.ValidateID("commit_id", commitID); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.RenameCommit(ctx, commitID, actorID, label, description)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *DocumentLogic) RestoreCommit(ctx context.Context, commitID string, actorID int64, key string) (*domain.Document, error) {
	if err := domain.ValidateID("commit_id", commitID); err != nil {
		return nil, mapError(err)
	}
	value, err := idempotency(actorID, "restore_commit", key, struct {
		CommitID string `json:"commit_id"`
	}{commitID})
	if err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.RestoreCommit(ctx, commitID, actorID, value)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func publicationMediaIDs(content domain.RichTextDocument) []string {
	seen := make(map[string]struct{})
	var visit func([]*domain.RichTextNode)
	visit = func(nodes []*domain.RichTextNode) {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if node.Attrs != nil && node.Attrs.AttachmentID != nil {
				seen[*node.Attrs.AttachmentID] = struct{}{}
			}
			visit(node.Content)
		}
	}
	visit(content.Content)
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
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

// PurgeDeleted permanently removes a document that has already been moved to
// the trash. The repository keeps this operation idempotent so an asynchronous
// cleanup retry can safely repeat the request.
func (l *DocumentLogic) PurgeDeleted(ctx context.Context, documentID string, actorID, expected int64, key string) error {
	if actorID <= 0 {
		return mapError(repository.ErrForbidden)
	}
	if err := validateMutation(documentID, expected); err != nil {
		return mapError(err)
	}
	idempotencyValue, err := idempotency(actorID, "purge_deleted_document", key, struct {
		DocumentID       string `json:"document_id"`
		ExpectedRevision int64  `json:"expected_revision"`
	}{documentID, expected})
	if err != nil {
		return mapError(err)
	}
	return mapError(l.repository.PurgeDeletedDocument(ctx, documentID, actorID, expected, idempotencyValue))
}

func validateListInput(input ListDocumentsInput, public bool) error {
	if err := domain.ValidatePage(input.Limit, input.Cursor, input.Query); err != nil {
		return err
	}
	if public && (input.Access != "" || input.Publication != "" || input.FolderID != "") {
		return &domain.ValidationError{Field: "filters", Reason: "access, publication, and folder are not valid for the public collection"}
	}
	if input.Access != "" && input.Access != domain.AccessOwner && input.Access != "shared" {
		return &domain.ValidationError{Field: "access", Reason: "must be owner or shared"}
	}
	if input.Publication != "" && input.Publication != "published" && input.Publication != "draft" {
		return &domain.ValidationError{Field: "publication", Reason: "must be published or draft"}
	}
	if input.FolderID != "" {
		if err := domain.ValidateID("folder_id", input.FolderID); err != nil {
			return err
		}
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

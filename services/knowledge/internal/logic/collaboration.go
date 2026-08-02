package logic

import (
	"context"
	"errors"
	"strings"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

type CollaborationRepository interface {
	AuthorizeCollaboration(context.Context, string, int64) (*domain.Document, error)
	UpsertProjection(context.Context, domain.Projection) error
}

type CollaborationLogic struct {
	repository CollaborationRepository
	directory  Directory
	now        func() time.Time
}

type CollaborationAuthorization struct {
	Document *domain.Document
	User     *domain.PublicUser
}

func NewCollaborationLogic(repository CollaborationRepository, directory Directory) (*CollaborationLogic, error) {
	if repository == nil || directory == nil {
		return nil, errors.New("create collaboration logic: repository and directory are required")
	}
	return &CollaborationLogic{repository: repository, directory: directory, now: time.Now}, nil
}

func (l *CollaborationLogic) Authorize(ctx context.Context, documentID string, actorID int64) (*CollaborationAuthorization, error) {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return nil, mapError(err)
	}
	var user *domain.PublicUser
	if actorID > 0 {
		resolved, err := l.directory.CurrentUser(ctx)
		if err != nil {
			return nil, mapError(err)
		}
		if resolved.ID != actorID {
			return nil, mapError(repository.ErrForbidden)
		}
		user = &resolved
	}
	document, err := l.repository.AuthorizeCollaboration(ctx, documentID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	return &CollaborationAuthorization{Document: document, User: user}, nil
}

func (l *CollaborationLogic) Project(
	ctx context.Context,
	documentID string,
	sequence int64,
	content domain.RichTextDocument,
	plainText string,
) error {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return mapError(err)
	}
	if sequence < 0 {
		return mapError(&domain.ValidationError{Field: "sequence", Reason: "must not be negative"})
	}
	if err := content.Validate(); err != nil {
		return mapError(err)
	}
	plainText = strings.TrimSpace(plainText)
	payload, err := jsoncodec.Marshal(content)
	if err != nil {
		return mapError(err)
	}
	if err := domain.ValidateProjection(payload, plainText); err != nil {
		return mapError(err)
	}
	if err := l.repository.UpsertProjection(ctx, domain.Projection{
		DocumentID: documentID, Sequence: sequence, Content: payload,
		PlainText: plainText, ProjectedAt: l.now().UTC(),
	}); err != nil {
		return mapError(err)
	}
	return nil
}

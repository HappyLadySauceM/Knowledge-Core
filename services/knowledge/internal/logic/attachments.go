package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/storage"
)

type AttachmentRepository interface {
	ListAttachments(context.Context, string, int64) ([]*domain.Attachment, error)
	CreateAttachment(context.Context, *domain.Attachment, int64, repository.Idempotency) (bool, error)
	GetAttachment(context.Context, string, string, int64, bool) (*domain.Attachment, error)
	GetAttachmentContent(context.Context, string, int64) (*domain.Attachment, error)
	MarkAttachmentScanning(context.Context, string, string, int64) (*domain.Attachment, error)
	QueueAttachmentDeletion(context.Context, string, string, int64) (*domain.Attachment, error)
}

type ObjectStore interface {
	PresignUpload(context.Context, string, string, string, int64, time.Time) (domain.UploadTarget, error)
	VerifyUpload(context.Context, string, string, int64) error
	PresignDownload(context.Context, string, string, string) (string, time.Time, error)
}

type AttachmentLogic struct {
	repository AttachmentRepository
	objects    ObjectStore
	uploadTTL  atomic.Int64
	now        func() time.Time
}

type CreateAttachmentInput struct {
	DocumentID     string
	ActorID        int64
	Filename       string
	MediaType      string
	SizeBytes      int64
	SHA256         string
	IdempotencyKey string
}

func NewAttachmentLogic(repository AttachmentRepository, objects ObjectStore, uploadTTL time.Duration) (*AttachmentLogic, error) {
	if repository == nil || objects == nil || uploadTTL <= 0 {
		return nil, errors.New("create attachment logic: repository, object storage, and positive upload TTL are required")
	}
	logic := &AttachmentLogic{repository: repository, objects: objects, now: time.Now}
	logic.uploadTTL.Store(int64(uploadTTL))
	return logic, nil
}

func (l *AttachmentLogic) List(ctx context.Context, documentID string, actorID int64) ([]*domain.Attachment, error) {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return nil, mapError(err)
	}
	result, err := l.repository.ListAttachments(ctx, documentID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *AttachmentLogic) Create(ctx context.Context, input CreateAttachmentInput) (*domain.AttachmentUpload, error) {
	if err := domain.ValidateID("document_id", input.DocumentID); err != nil {
		return nil, mapError(err)
	}
	input.Filename = strings.TrimSpace(input.Filename)
	input.MediaType = strings.TrimSpace(input.MediaType)
	input.SHA256 = strings.TrimSpace(input.SHA256)
	if err := domain.ValidateAttachment(input.Filename, input.MediaType, input.SizeBytes, input.SHA256); err != nil {
		return nil, mapError(err)
	}
	idempotencyValue, err := idempotency(input.ActorID, "create_attachment", input.IdempotencyKey, struct {
		DocumentID string `json:"document_id"`
		Filename   string `json:"filename"`
		MediaType  string `json:"media_type"`
		SizeBytes  int64  `json:"size_bytes"`
		SHA256     string `json:"sha256"`
	}{input.DocumentID, input.Filename, input.MediaType, input.SizeBytes, input.SHA256})
	if err != nil {
		return nil, mapError(err)
	}
	id, err := domain.NewID()
	if err != nil {
		return nil, mapError(err)
	}
	now := l.now().UTC()
	attachment := &domain.Attachment{
		ID: id, DocumentID: input.DocumentID, UploaderID: input.ActorID,
		Filename: input.Filename, DeclaredType: input.MediaType, SizeBytes: input.SizeBytes,
		SHA256: input.SHA256, ObjectKey: fmt.Sprintf("quarantine/%d/%s/%s", input.ActorID, input.DocumentID, id),
		Status: domain.AttachmentPendingUpload, UploadExpires: now.Add(time.Duration(l.uploadTTL.Load())), CreatedAt: now, UpdatedAt: now,
	}
	_, err = l.repository.CreateAttachment(ctx, attachment, input.ActorID, idempotencyValue)
	if err != nil {
		return nil, mapError(err)
	}
	if !attachment.UploadExpires.After(l.now().UTC().Add(time.Second)) {
		return nil, knowledgeerrors.Conflict.New()
	}
	target, err := l.objects.PresignUpload(
		ctx, attachment.ObjectKey, attachment.DeclaredType, attachment.SHA256,
		attachment.SizeBytes, attachment.UploadExpires,
	)
	if err != nil {
		return nil, knowledgeerrors.Unavailable.Wrap(err)
	}
	return &domain.AttachmentUpload{
		Attachment: attachment, URL: target.URL, RequiredHeaders: target.RequiredHeaders, ExpiresAt: target.ExpiresAt,
	}, nil
}

func (l *AttachmentLogic) SetUploadTTL(ttl time.Duration) {
	if l != nil {
		l.uploadTTL.Store(int64(ttl))
	}
}

func (l *AttachmentLogic) Complete(ctx context.Context, documentID, attachmentID string, actorID int64) (*domain.Attachment, error) {
	if err := validateAttachmentPath(documentID, attachmentID); err != nil {
		return nil, mapError(err)
	}
	attachment, err := l.repository.GetAttachment(ctx, documentID, attachmentID, actorID, true)
	if err != nil {
		return nil, mapError(err)
	}
	if attachment.Status != domain.AttachmentPendingUpload || !attachment.UploadExpires.After(l.now().UTC()) {
		return nil, knowledgeerrors.Conflict.New()
	}
	if err := l.objects.VerifyUpload(ctx, attachment.ObjectKey, attachment.SHA256, attachment.SizeBytes); err != nil {
		if errors.Is(err, storage.ErrObjectMismatch) {
			return nil, knowledgeerrors.Conflict.Wrap(err)
		}
		return nil, knowledgeerrors.Unavailable.Wrap(err)
	}
	result, err := l.repository.MarkAttachmentScanning(ctx, documentID, attachmentID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	return result, nil
}

func (l *AttachmentLogic) Delete(ctx context.Context, documentID, attachmentID string, actorID int64) error {
	if err := validateAttachmentPath(documentID, attachmentID); err != nil {
		return mapError(err)
	}
	if _, err := l.repository.QueueAttachmentDeletion(ctx, documentID, attachmentID, actorID); err != nil {
		return mapError(err)
	}
	return nil
}

func (l *AttachmentLogic) Content(ctx context.Context, attachmentID string, actorID int64) (*domain.AttachmentContent, error) {
	if err := domain.ValidateID("attachment_id", attachmentID); err != nil {
		return nil, mapError(err)
	}
	attachment, err := l.repository.GetAttachmentContent(ctx, attachmentID, actorID)
	if err != nil {
		return nil, mapError(err)
	}
	mediaType := attachment.DetectedType
	if mediaType == "" {
		mediaType = attachment.DeclaredType
	}
	url, expiresAt, err := l.objects.PresignDownload(ctx, attachment.ObjectKey, attachment.Filename, mediaType)
	if err != nil {
		return nil, knowledgeerrors.Unavailable.Wrap(err)
	}
	return &domain.AttachmentContent{URL: url, ExpiresAt: expiresAt}, nil
}

func validateAttachmentPath(documentID, attachmentID string) error {
	if err := domain.ValidateID("document_id", documentID); err != nil {
		return err
	}
	return domain.ValidateID("attachment_id", attachmentID)
}

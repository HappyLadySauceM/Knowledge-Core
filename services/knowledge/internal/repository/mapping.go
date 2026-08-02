package repository

import (
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

func documentToModel(value *domain.Document) *model.Document {
	return &model.Document{
		ID: value.ID, Title: value.Title, Summary: value.Summary, Slug: value.Slug,
		OwnerID: value.Owner.ID, OwnerUsername: value.Owner.Username, OwnerAvatar: value.Owner.Avatar,
		Published: value.Published, MetadataRevision: value.MetadataRevision, ContentRevision: value.ContentRevision,
		PermissionRevision: value.PermissionRevision, PublishedAt: value.PublishedAt,
		DeletedAt: value.DeletedAt, PurgeAfter: value.PurgeAfter, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(),
	}
}

func documentFromModel(value *model.Document, access string, projection *model.Projection) *domain.Document {
	if value == nil {
		return nil
	}
	result := &domain.Document{
		ID: value.ID, Title: value.Title, Summary: value.Summary, Slug: value.Slug,
		Owner:  domain.PublicUser{ID: value.OwnerID, Username: value.OwnerUsername, Avatar: value.OwnerAvatar},
		Access: access, Published: value.Published, MetadataRevision: value.MetadataRevision,
		ContentRevision: value.ContentRevision, PermissionRevision: value.PermissionRevision,
		PublishedAt: value.PublishedAt, DeletedAt: value.DeletedAt, PurgeAfter: value.PurgeAfter,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
	if projection != nil && !projection.ProjectedAt.IsZero() {
		projectedAt := projection.ProjectedAt
		result.ProjectedAt = &projectedAt
	}
	return result
}

func projectionFromModel(value *model.Projection) *domain.Projection {
	if value == nil {
		return nil
	}
	return &domain.Projection{
		DocumentID: value.DocumentID, Sequence: value.Sequence,
		Content: append([]byte(nil), value.Content...), PlainText: value.PlainText, ProjectedAt: value.ProjectedAt,
	}
}

func memberFromModel(value *model.Member) *domain.Member {
	if value == nil {
		return nil
	}
	return &domain.Member{
		DocumentID: value.DocumentID,
		User:       domain.PublicUser{ID: value.UserID, Username: value.Username, Avatar: value.Avatar},
		Role:       value.Role,
		Revision:   value.Revision,
		CreatedAt:  value.CreatedAt,
		UpdatedAt:  value.UpdatedAt,
	}
}

func attachmentFromModel(value *model.Attachment) *domain.Attachment {
	if value == nil {
		return nil
	}
	return &domain.Attachment{
		ID: value.ID, DocumentID: value.DocumentID, UploaderID: value.UploaderID, Filename: value.Filename,
		DeclaredType: value.DeclaredType, DetectedType: value.DetectedType, SizeBytes: value.SizeBytes,
		SHA256: value.SHA256, ObjectKey: value.ObjectKey, Status: value.Status, FailureReason: value.FailureReason,
		UploadExpires: value.UploadExpires, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func mapNotFound(operation string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func mapWriteError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && (postgresError.Code == "23505" || postgresError.Code == "23503" || postgresError.Code == "23514") {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

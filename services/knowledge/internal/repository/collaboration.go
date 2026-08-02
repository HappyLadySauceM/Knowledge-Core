package repository

import (
	"bytes"
	"context"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) AuthorizeCollaboration(ctx context.Context, documentID string, actorID int64) (*domain.Document, error) {
	document, err := s.GetDocument(ctx, documentID, actorID, false)
	if err != nil {
		return nil, err
	}
	if domain.CanRead(document.Access) {
		return document, nil
	}
	if document.Published {
		document.Access = domain.AccessViewer
		return document, nil
	}
	return nil, ErrForbidden
}

// UpsertProjection advances the read-model sequence monotonically. The CRDT
// head remains owned by Collaboration; Knowledge only stores the latest
// validated REST/search projection it has received.
func (s *Store) UpsertProjection(ctx context.Context, projection domain.Projection) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		document, _, err := lockDocument(tx, projection.DocumentID, 0, false)
		if err != nil {
			return err
		}
		var current model.Projection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("document_id = ?", projection.DocumentID).First(&current).Error; err != nil {
			return mapNotFound("lock document projection", err)
		}
		if projection.Sequence < current.Sequence {
			return ErrPrecondition
		}
		if projection.Sequence == current.Sequence {
			if bytes.Equal(current.Content, projection.Content) && current.PlainText == projection.PlainText {
				return nil
			}
			return ErrConflict
		}
		if err := tx.Model(&current).Updates(map[string]any{
			"sequence": projection.Sequence, "content": projection.Content,
			"plain_text": projection.PlainText, "projected_at": projection.ProjectedAt.UTC(),
		}).Error; err != nil {
			return fmt.Errorf("update document projection: %w", err)
		}
		if err := tx.Model(document).Updates(map[string]any{
			"content_revision": projection.Sequence,
			"updated_at":       projection.ProjectedAt.UTC(),
		}).Error; err != nil {
			return fmt.Errorf("advance projected document sequence: %w", err)
		}
		return nil
	})
}

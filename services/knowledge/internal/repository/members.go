package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
)

func (s *Store) ListMembers(ctx context.Context, documentID string, actorID int64) ([]*domain.Member, error) {
	document, access, err := lockDocument(s.db.WithContext(ctx), documentID, actorID, false)
	if err != nil {
		return nil, err
	}
	if document == nil || access != domain.AccessOwner {
		return nil, ErrForbidden
	}
	var records []model.Member
	if err := s.db.WithContext(ctx).Where("document_id = ?", documentID).Order("created_at ASC, user_id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list document members: %w", err)
	}
	result := make([]*domain.Member, 0, len(records))
	for index := range records {
		result = append(result, memberFromModel(&records[index]))
	}
	return result, nil
}

func (s *Store) AddMember(
	ctx context.Context,
	documentID string,
	actorID int64,
	user domain.PublicUser,
	role string,
	idempotency Idempotency,
) (*domain.Member, error) {
	var result *domain.Member
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		document, access, err := lockDocument(tx, documentID, actorID, false)
		if err != nil {
			return err
		}
		if access != domain.AccessOwner {
			return ErrForbidden
		}
		if user.ID == document.OwnerID {
			return ErrConflict
		}
		_, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			var existing model.Member
			if err := tx.Where("document_id = ? AND user_id = ?", documentID, user.ID).First(&existing).Error; err != nil {
				return mapNotFound("load idempotent member", err)
			}
			result = memberFromModel(&existing)
			return nil
		}
		now := s.now().UTC()
		record := model.Member{
			DocumentID: documentID, UserID: user.ID, Username: user.Username, Avatar: user.Avatar,
			Role: role, Revision: 1, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&record).Error; err != nil {
			return mapWriteError("add document member", err)
		}
		if err := s.advancePermissionRevision(tx, document, now); err != nil {
			return err
		}
		if err := s.saveIdempotency(tx, idempotency, documentID); err != nil {
			return err
		}
		result = memberFromModel(&record)
		return nil
	})
	return result, err
}

func (s *Store) UpdateMember(ctx context.Context, documentID string, actorID, userID, expected int64, role string) (*domain.Member, error) {
	var result *domain.Member
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		document, access, err := lockDocument(tx, documentID, actorID, false)
		if err != nil {
			return err
		}
		if access != domain.AccessOwner {
			return ErrForbidden
		}
		var record model.Member
		if err := tx.Where("document_id = ? AND user_id = ?", documentID, userID).First(&record).Error; err != nil {
			return mapNotFound("get document member", err)
		}
		if record.Revision != expected {
			return ErrPrecondition
		}
		now := s.now().UTC()
		if err := tx.Model(&record).Updates(map[string]any{
			"role": role, "revision": gorm.Expr("revision + 1"), "updated_at": now,
		}).Error; err != nil {
			return mapWriteError("update document member", err)
		}
		record.Role = role
		record.Revision++
		record.UpdatedAt = now
		if err := s.advancePermissionRevision(tx, document, now); err != nil {
			return err
		}
		result = memberFromModel(&record)
		return nil
	})
	return result, err
}

func (s *Store) DeleteMember(ctx context.Context, documentID string, actorID, userID, expected int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		document, access, err := lockDocument(tx, documentID, actorID, false)
		if err != nil {
			return err
		}
		if access != domain.AccessOwner {
			return ErrForbidden
		}
		result := tx.Where("document_id = ? AND user_id = ? AND revision = ?", documentID, userID, expected).Delete(&model.Member{})
		if result.Error != nil {
			return fmt.Errorf("delete document member: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			var count int64
			if err := tx.Model(&model.Member{}).Where("document_id = ? AND user_id = ?", documentID, userID).Count(&count).Error; err != nil {
				return fmt.Errorf("check document member revision: %w", err)
			}
			if count == 0 {
				return ErrNotFound
			}
			return ErrPrecondition
		}
		return s.advancePermissionRevision(tx, document, s.now().UTC())
	})
}

func (s *Store) advancePermissionRevision(tx *gorm.DB, document *model.Document, now time.Time) error {
	if err := tx.Model(document).Updates(map[string]any{
		"permission_revision": gorm.Expr("permission_revision + 1"), "updated_at": now,
	}).Error; err != nil {
		return fmt.Errorf("advance document permission revision: %w", err)
	}
	document.PermissionRevision++
	document.UpdatedAt = now
	return s.enqueuePermissionChanged(tx, document, now)
}

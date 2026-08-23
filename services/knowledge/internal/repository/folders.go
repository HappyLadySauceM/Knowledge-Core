package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Store) ListFolders(ctx context.Context, ownerID int64, parentID *string) ([]*domain.Folder, error) {
	var rows []model.Folder
	query := s.db.WithContext(ctx).Where("owner_id = ?", ownerID)
	if parentID == nil {
		query = query.Where("parent_id IS NULL")
	} else {
		query = query.Where("parent_id = ?", *parentID)
	}
	if err := query.Order("lower(name) ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list knowledge folders: %w", err)
	}
	result := make([]*domain.Folder, 0, len(rows))
	for index := range rows {
		result = append(result, folderFromModel(&rows[index]))
	}
	return result, nil
}

func (s *Store) CreateFolder(ctx context.Context, ownerID int64, name string, parentID *string, idempotency Idempotency) (*domain.Folder, error) {
	var result *domain.Folder
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			var row model.Folder
			if err := tx.Where("id = ? AND owner_id = ?", existingID, ownerID).First(&row).Error; err != nil {
				return mapNotFound("load idempotent folder", err)
			}
			result = folderFromModel(&row)
			return nil
		}
		depth := int32(1)
		if parentID != nil {
			var parent model.Folder
			if err := tx.Where("id = ? AND owner_id = ?", *parentID, ownerID).First(&parent).Error; err != nil {
				return mapNotFound("get parent folder", err)
			}
			depth = int32(parent.Depth + 1)
			if depth > 8 {
				return &domain.ValidationError{Field: "parent_id", Reason: "folder depth cannot exceed 8"}
			}
		}
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		row := &model.Folder{ID: id, OwnerID: ownerID, ParentID: parentID, Name: name, Depth: int(depth), Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(row).Error; err != nil {
			return mapWriteError("create knowledge folder", err)
		}
		if err := s.saveIdempotency(tx, idempotency, id); err != nil {
			return err
		}
		result = folderFromModel(row)
		return nil
	})
	return result, err
}

func (s *Store) UpdateFolder(ctx context.Context, ownerID int64, id string, expected int64, name, parentID *string) (*domain.Folder, error) {
	var result *domain.Folder
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Folder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", id, ownerID).First(&row).Error; err != nil {
			return mapNotFound("get knowledge folder", err)
		}
		if row.Revision != expected {
			return ErrPrecondition
		}
		if name == nil && parentID == nil {
			return &domain.ValidationError{Field: "folder", Reason: "at least one field must be provided"}
		}
		updates := map[string]any{"revision": gorm.Expr("revision + 1"), "updated_at": s.now().UTC()}
		if name != nil {
			updates["name"] = *name
		}
		if parentID != nil {
			if *parentID == id {
				return ErrConflict
			}
			newDepth := int32(1)
			if *parentID != "" {
				var cycle bool
				if err := tx.Raw(`WITH RECURSIVE descendants AS (
  SELECT id FROM knowledge.folders WHERE id = ? AND owner_id = ?
  UNION ALL
  SELECT child.id FROM knowledge.folders child JOIN descendants parent ON child.parent_id = parent.id WHERE child.owner_id = ?
)
SELECT EXISTS (SELECT 1 FROM descendants WHERE id = ?)`, id, ownerID, ownerID, *parentID).Scan(&cycle).Error; err != nil {
					return fmt.Errorf("check folder cycle: %w", err)
				}
				if cycle {
					return ErrConflict
				}
				var parent model.Folder
				if err := tx.Where("id = ? AND owner_id = ?", *parentID, ownerID).First(&parent).Error; err != nil {
					return mapNotFound("get destination folder", err)
				}
				newDepth = int32(parent.Depth + 1)
			}
			if newDepth > 8 {
				return &domain.ValidationError{Field: "parent_id", Reason: "folder depth cannot exceed 8"}
			}
			updates["parent_id"] = nullableFolderID(*parentID)
			updates["depth"] = newDepth
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return mapWriteError("update knowledge folder", err)
		}
		if err := tx.Where("id = ?", id).First(&row).Error; err != nil {
			return err
		}
		result = folderFromModel(&row)
		return nil
	})
	return result, err
}

func (s *Store) DeleteFolder(ctx context.Context, ownerID int64, id string, expected int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.Folder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_id = ?", id, ownerID).First(&row).Error; err != nil {
			return mapNotFound("get knowledge folder", err)
		}
		if row.Revision != expected {
			return ErrPrecondition
		}
		var parentID any
		if row.ParentID != nil {
			parentID = *row.ParentID
		}
		if err := tx.Model(&model.Folder{}).Where("owner_id = ? AND parent_id = ?", ownerID, id).Updates(map[string]any{"parent_id": parentID, "depth": gorm.Expr("GREATEST(depth - 1, 1)"), "updated_at": s.now().UTC()}).Error; err != nil {
			return fmt.Errorf("reparent child folders: %w", err)
		}
		if err := tx.Model(&model.DocumentPlacement{}).Where("owner_id = ? AND folder_id = ?", ownerID, id).Updates(map[string]any{"folder_id": parentID, "revision": gorm.Expr("revision + 1"), "updated_at": s.now().UTC()}).Error; err != nil {
			return fmt.Errorf("reparent folder documents: %w", err)
		}
		if err := tx.Delete(&row).Error; err != nil {
			return fmt.Errorf("delete knowledge folder: %w", err)
		}
		return nil
	})
}

func folderFromModel(value *model.Folder) *domain.Folder {
	return &domain.Folder{ID: value.ID, ParentID: value.ParentID, Name: value.Name, Depth: int32(value.Depth), Revision: value.Revision, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

func nullableFolderID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

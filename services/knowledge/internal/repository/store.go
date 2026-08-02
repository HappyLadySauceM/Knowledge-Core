package repository

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound      = errors.New("knowledge repository: resource not found")
	ErrGone          = errors.New("knowledge repository: resource is gone")
	ErrConflict      = errors.New("knowledge repository: resource conflict")
	ErrPrecondition  = errors.New("knowledge repository: precondition failed")
	ErrForbidden     = errors.New("knowledge repository: permission denied")
	ErrQuotaExceeded = errors.New("knowledge repository: storage quota exceeded")
)

const permissionChangedSubject = "knowledge.permissions.changed"

type Store struct {
	db  *gorm.DB
	now func() time.Time
}

func NewStore(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("create knowledge store: database is required")
	}
	return &Store{db: db, now: time.Now}, nil
}

type Cursor struct {
	Version int       `json:"v"`
	Time    time.Time `json:"time"`
	ID      string    `json:"id"`
}

type Idempotency struct {
	ActorID     int64
	Operation   string
	Key         string
	RequestHash string
}

func EncodeCursor(value Cursor) (string, error) {
	value.Version = 1
	payload, err := jsoncodec.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func DecodeCursor(value string) (*Cursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode cursor: %w", err)
	}
	var result Cursor
	if err := jsoncodec.Unmarshal(payload, &result); err != nil || result.Version != 1 || result.Time.IsZero() || domain.ValidateID("cursor.id", result.ID) != nil {
		return nil, errors.New("decode cursor: invalid cursor")
	}
	return &result, nil
}

func (s *Store) CreateDocument(ctx context.Context, document *domain.Document, idempotency Idempotency) error {
	if document == nil {
		return errors.New("create document: document is required")
	}
	record := documentToModel(document)
	projection := model.Projection{
		DocumentID:  record.ID,
		Sequence:    0,
		Content:     []byte(`{"type":"doc","content":[{"type":"paragraph"}]}`),
		PlainText:   "",
		ProjectedAt: record.CreatedAt,
	}
	var stored *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			existing, loadErr := s.getDocument(tx, existingID, idempotency.ActorID, false)
			if loadErr != nil {
				return fmt.Errorf("load idempotent document: %w", loadErr)
			}
			stored = existing
			return nil
		}
		if err := tx.Create(record).Error; err != nil {
			return mapWriteError("insert document", err)
		}
		if err := tx.Create(&model.SlugAlias{Slug: record.Slug, DocumentID: stringPointer(record.ID), CreatedAt: record.CreatedAt}).Error; err != nil {
			return mapWriteError("reserve document slug", err)
		}
		if err := tx.Create(&projection).Error; err != nil {
			return fmt.Errorf("insert initial document projection: %w", err)
		}
		if err := s.saveIdempotency(tx, idempotency, record.ID); err != nil {
			return err
		}
		stored = documentFromModel(record, domain.AccessOwner, &projection)
		return nil
	})
	if err != nil {
		return err
	}
	*document = *stored
	return nil
}

func (s *Store) GetDocument(ctx context.Context, id string, actorID int64, includeDeleted bool) (*domain.Document, error) {
	return s.getDocument(s.db.WithContext(ctx), id, actorID, includeDeleted)
}

func (s *Store) getDocument(db *gorm.DB, id string, actorID int64, includeDeleted bool) (*domain.Document, error) {
	var record model.Document
	query := db.Where("id = ?", id)
	if !includeDeleted {
		query = query.Where("deleted_at IS NULL")
	}
	if err := query.First(&record).Error; err != nil {
		return nil, mapNotFound("get document", err)
	}
	access, err := accessFor(db, &record, actorID)
	if err != nil {
		return nil, err
	}
	projection, err := getProjection(db, record.ID)
	if err != nil {
		return nil, err
	}
	return documentFromModel(&record, access, projection), nil
}

func (s *Store) GetPublishedDocument(ctx context.Context, slug string, actorID int64) (*domain.Document, *domain.Projection, bool, error) {
	var alias model.SlugAlias
	if err := s.db.WithContext(ctx).Where("lower(slug) = lower(?)", slug).First(&alias).Error; err != nil {
		return nil, nil, false, mapNotFound("resolve published document slug", err)
	}
	if alias.DocumentID == nil {
		return nil, nil, false, ErrGone
	}
	var record model.Document
	if err := s.db.WithContext(ctx).Where("id = ?", *alias.DocumentID).First(&record).Error; err != nil {
		return nil, nil, false, mapNotFound("get published document", err)
	}
	if record.DeletedAt != nil {
		return nil, nil, false, ErrGone
	}
	if !record.Published {
		return nil, nil, false, ErrNotFound
	}
	access, err := accessFor(s.db.WithContext(ctx), &record, actorID)
	if err != nil {
		return nil, nil, false, err
	}
	projection, err := getProjection(s.db.WithContext(ctx), record.ID)
	if err != nil {
		return nil, nil, false, err
	}
	document := documentFromModel(&record, access, projection)
	return document, projectionFromModel(projection), !strings.EqualFold(record.Slug, slug), nil
}

type ListOptions struct {
	ActorID     int64
	Query       string
	Cursor      *Cursor
	Limit       int
	Access      string
	Publication string
	Published   bool
	Deleted     bool
}

func (s *Store) ListDocuments(ctx context.Context, options ListOptions) ([]*domain.Document, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 20
	}
	type row struct {
		model.Document
		Access      string     `gorm:"column:access"`
		ProjectedAt *time.Time `gorm:"column:projected_at"`
	}
	selectSQL := `SELECT d.*, p.projected_at, CASE WHEN d.owner_id = ? THEN 'owner' ELSE COALESCE(m.role, 'none') END AS access
FROM knowledge.documents d
LEFT JOIN knowledge.document_members m ON m.document_id = d.id AND m.user_id = ?
LEFT JOIN knowledge.document_projections p ON p.document_id = d.id`
	args := []any{options.ActorID, options.ActorID}
	conditions := make([]string, 0, 8)
	switch {
	case options.Published:
		conditions = append(conditions, "d.published = true", "d.deleted_at IS NULL")
	case options.Deleted:
		conditions = append(conditions, "d.deleted_at IS NOT NULL", "d.owner_id = ?")
		args = append(args, options.ActorID)
	default:
		conditions = append(conditions, "d.deleted_at IS NULL", "(d.owner_id = ? OR m.user_id IS NOT NULL)")
		args = append(args, options.ActorID)
	}
	if options.Query != "" {
		conditions = append(conditions, "(d.title ILIKE ? ESCAPE '\\' OR d.summary ILIKE ? ESCAPE '\\' OR COALESCE(p.plain_text, '') ILIKE ? ESCAPE '\\')")
		pattern := "%" + escapeLike(options.Query) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	switch options.Access {
	case domain.AccessOwner:
		conditions = append(conditions, "d.owner_id = ?")
		args = append(args, options.ActorID)
	case "shared":
		conditions = append(conditions, "m.user_id IS NOT NULL")
	}
	switch options.Publication {
	case "published":
		conditions = append(conditions, "d.published = true")
	case "draft":
		conditions = append(conditions, "d.published = false")
	}
	orderColumn := "d.updated_at"
	if options.Published {
		orderColumn = "d.published_at"
	} else if options.Deleted {
		orderColumn = "d.deleted_at"
	}
	if options.Cursor != nil {
		conditions = append(conditions, fmt.Sprintf("(%s, d.id) < (?, ?::uuid)", orderColumn))
		args = append(args, options.Cursor.Time, options.Cursor.ID)
	}
	if len(conditions) > 0 {
		selectSQL += " WHERE " + strings.Join(conditions, " AND ")
	}
	selectSQL += fmt.Sprintf(" ORDER BY %s DESC, d.id DESC LIMIT ?", orderColumn)
	args = append(args, limit+1)
	var rows []row
	if err := s.db.WithContext(ctx).Raw(selectSQL, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list knowledge documents: %w", err)
	}
	result := make([]*domain.Document, 0, len(rows))
	for index := range rows {
		projection := &model.Projection{DocumentID: rows[index].ID, Sequence: rows[index].ContentRevision}
		if rows[index].ProjectedAt != nil {
			projection.ProjectedAt = *rows[index].ProjectedAt
		}
		result = append(result, documentFromModel(&rows[index].Document, rows[index].Access, projection))
	}
	return result, nil
}

func (s *Store) UpdateDocument(ctx context.Context, id string, actorID, expected int64, title, summary, slug *string) (*domain.Document, error) {
	var result *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, access, err := lockDocument(tx, id, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		if record.MetadataRevision != expected {
			return ErrPrecondition
		}
		now := s.now().UTC()
		updates := map[string]any{"metadata_revision": gorm.Expr("metadata_revision + 1"), "updated_at": now}
		if title != nil {
			updates["title"] = *title
		}
		if summary != nil {
			updates["summary"] = *summary
		}
		if slug != nil && *slug != record.Slug {
			if err := tx.Create(&model.SlugAlias{Slug: *slug, DocumentID: stringPointer(id), CreatedAt: now}).Error; err != nil {
				return mapWriteError("reserve updated document slug", err)
			}
			updates["slug"] = *slug
		}
		if err := tx.Model(record).Updates(updates).Error; err != nil {
			return mapWriteError("update document", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload updated document: %w", err)
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		return nil
	})
	return result, err
}

func (s *Store) SetPublication(ctx context.Context, id string, actorID, expected int64, published bool) (*domain.Document, error) {
	var result *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, access, err := lockDocument(tx, id, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		if record.MetadataRevision != expected {
			return ErrPrecondition
		}
		now := s.now().UTC()
		updates := map[string]any{
			"published": published, "metadata_revision": gorm.Expr("metadata_revision + 1"),
			"permission_revision": gorm.Expr("permission_revision + 1"), "updated_at": now,
		}
		if published {
			updates["published_at"] = now
		} else {
			updates["published_at"] = nil
		}
		if err := tx.Model(record).Updates(updates).Error; err != nil {
			return fmt.Errorf("set document publication: %w", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload document publication: %w", err)
		}
		if err := s.enqueuePermissionChanged(tx, record, now); err != nil {
			return err
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		return nil
	})
	return result, err
}

func (s *Store) SoftDeleteDocument(ctx context.Context, id string, actorID, expected int64) (*domain.Document, error) {
	var result *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, access, err := lockDocument(tx, id, actorID, false)
		if err != nil {
			return err
		}
		if access != domain.AccessOwner {
			return ErrForbidden
		}
		if record.MetadataRevision != expected {
			return ErrPrecondition
		}
		now := s.now().UTC()
		if err := tx.Model(record).Updates(map[string]any{
			"published": false, "published_at": nil, "deleted_at": now, "purge_after": now.Add(30 * 24 * time.Hour),
			"metadata_revision": gorm.Expr("metadata_revision + 1"), "permission_revision": gorm.Expr("permission_revision + 1"), "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("soft-delete document: %w", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload deleted document: %w", err)
		}
		if err := s.enqueuePermissionChanged(tx, record, now); err != nil {
			return err
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		return nil
	})
	return result, err
}

func (s *Store) RestoreDeletedDocument(ctx context.Context, id string, actorID int64) (*domain.Document, error) {
	var result *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, access, err := lockDocument(tx, id, actorID, true)
		if err != nil {
			return err
		}
		if access != domain.AccessOwner || record.DeletedAt == nil {
			return ErrForbidden
		}
		now := s.now().UTC()
		if err := tx.Model(record).Updates(map[string]any{
			"deleted_at": nil, "purge_after": nil, "metadata_revision": gorm.Expr("metadata_revision + 1"),
			"permission_revision": gorm.Expr("permission_revision + 1"), "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("restore deleted document: %w", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload restored document: %w", err)
		}
		if err := s.enqueuePermissionChanged(tx, record, now); err != nil {
			return err
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		return nil
	})
	return result, err
}

func (s *Store) GetProjection(ctx context.Context, id string) (*domain.Projection, error) {
	projection, err := getProjection(s.db.WithContext(ctx), id)
	if err != nil {
		return nil, err
	}
	return projectionFromModel(projection), nil
}

func accessFor(db *gorm.DB, document *model.Document, actorID int64) (string, error) {
	if actorID <= 0 {
		return domain.AccessNone, nil
	}
	if document.OwnerID == actorID {
		return domain.AccessOwner, nil
	}
	var member model.Member
	if err := db.Where("document_id = ? AND user_id = ?", document.ID, actorID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return domain.AccessNone, nil
		}
		return "", fmt.Errorf("get document access: %w", err)
	}
	return member.Role, nil
}

func lockDocument(tx *gorm.DB, id string, actorID int64, includeDeleted bool) (*model.Document, string, error) {
	var record model.Document
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id)
	if !includeDeleted {
		query = query.Where("deleted_at IS NULL")
	}
	if err := query.First(&record).Error; err != nil {
		return nil, "", mapNotFound("lock document", err)
	}
	access, err := accessFor(tx, &record, actorID)
	if err != nil {
		return nil, "", err
	}
	return &record, access, nil
}

func getProjection(db *gorm.DB, id string) (*model.Projection, error) {
	var result model.Projection
	if err := db.Where("document_id = ?", id).First(&result).Error; err != nil {
		return nil, mapNotFound("get document projection", err)
	}
	return &result, nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(value))
}

func stringPointer(value string) *string { return &value }

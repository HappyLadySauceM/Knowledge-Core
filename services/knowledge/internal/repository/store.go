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

type PublicationSnapshotInput struct {
	VersionID       string
	VersionSequence int64
	Title           string
	Summary         string
	Slug            string
	Language        string
	Tags            []string
	Content         domain.RichTextDocument
	PlainText       string
	Idempotency     Idempotency
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
	document := documentFromModel(&record, access, projection)
	if err := s.loadDocumentStructure(db, document); err != nil {
		return nil, err
	}
	return document, nil
}

func (s *Store) GetPublishedDocument(ctx context.Context, slug string, actorID int64) (*domain.Document, *domain.Projection, bool, error) {
	var publication model.DocumentPublication
	publicationErr := s.db.WithContext(ctx).Where("lower(slug) = lower(?)", slug).First(&publication).Error
	if publicationErr == nil {
		return s.publishedSnapshot(ctx, &publication, slug, actorID)
	}
	if !errors.Is(publicationErr, gorm.ErrRecordNotFound) {
		return nil, nil, false, fmt.Errorf("resolve published snapshot slug: %w", publicationErr)
	}
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
	if err := s.db.WithContext(ctx).Where("document_id = ?", record.ID).First(&publication).Error; err == nil {
		return s.publishedSnapshot(ctx, &publication, slug, actorID)
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

func (s *Store) publishedSnapshot(ctx context.Context, publication *model.DocumentPublication, requestedSlug string, actorID int64) (*domain.Document, *domain.Projection, bool, error) {
	var record model.Document
	if err := s.db.WithContext(ctx).Where("id = ?", publication.DocumentID).First(&record).Error; err != nil {
		return nil, nil, false, mapNotFound("get published snapshot document", err)
	}
	if record.DeletedAt != nil || !record.Published {
		return nil, nil, false, ErrNotFound
	}
	access, err := accessFor(s.db.WithContext(ctx), &record, actorID)
	if err != nil {
		return nil, nil, false, err
	}
	projection := &model.Projection{DocumentID: publication.DocumentID, Sequence: publication.VersionSequence, Content: append([]byte(nil), publication.Content...), PlainText: publication.PlainText, ProjectedAt: publication.UpdatedAt}
	document := documentFromModel(&record, access, projection)
	document.Title, document.Summary, document.Slug, document.Language = publication.Title, publication.Summary, publication.Slug, publication.Language
	document.PublishedAt = &publication.PublishedAt
	document.Published = true
	document.Tags = publicationTags(publication.Tags)
	return document, projectionFromModel(projection), !strings.EqualFold(publication.Slug, requestedSlug), nil
}

func publicationTags(value []byte) []string {
	var tags []string
	if len(value) == 0 || jsoncodec.Unmarshal(value, &tags) != nil {
		return nil
	}
	return tags
}

func (s *Store) loadDocumentStructure(db *gorm.DB, document *domain.Document) error {
	if document == nil {
		return nil
	}
	var placement model.DocumentPlacement
	if err := db.Where("owner_id = ? AND document_id = ?", document.Owner.ID, document.ID).First(&placement).Error; err == nil {
		document.FolderID = placement.FolderID
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("load document folder: %w", err)
	}
	var tags []string
	if err := db.Table("knowledge.tags").Select("knowledge.tags.name").Joins("JOIN knowledge.document_tags ON knowledge.document_tags.tag_id = knowledge.tags.id").Where("knowledge.document_tags.document_id = ?", document.ID).Order("knowledge.tags.name ASC").Pluck("knowledge.tags.name", &tags).Error; err != nil {
		return fmt.Errorf("load document tags: %w", err)
	}
	document.Tags = tags
	return nil
}

func (s *Store) replaceDocumentTags(tx *gorm.DB, ownerID int64, documentID string, tags []string, now time.Time) error {
	if err := tx.Where("document_id = ?", documentID).Delete(&model.DocumentTag{}).Error; err != nil {
		return fmt.Errorf("clear document tags: %w", err)
	}
	for _, name := range tags {
		slug := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), "-"))
		var tag model.Tag
		err := tx.Where("owner_id = ? AND slug = ?", ownerID, slug).First(&tag).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			id, idErr := domain.NewID()
			if idErr != nil {
				return idErr
			}
			tag = model.Tag{ID: id, OwnerID: ownerID, Name: name, Slug: slug, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&tag).Error; err != nil {
				return mapWriteError("create document tag", err)
			}
		} else if err != nil {
			return fmt.Errorf("get document tag: %w", err)
		}
		if err := tx.Create(&model.DocumentTag{DocumentID: documentID, TagID: tag.ID, CreatedAt: now}).Error; err != nil {
			return mapWriteError("attach document tag", err)
		}
	}
	return nil
}

func (s *Store) PublishSnapshot(ctx context.Context, id string, actorID, expected int64, input PublicationSnapshotInput) (*domain.Document, error) {
	var result *domain.Document
	content, err := jsoncodec.Marshal(input.Content)
	if err != nil {
		return nil, fmt.Errorf("encode publication snapshot: %w", err)
	}
	tags, err := jsoncodec.Marshal(input.Tags)
	if err != nil {
		return nil, fmt.Errorf("encode publication tags: %w", err)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, input.Idempotency)
		if err != nil {
			return err
		}
		if found {
			document, loadErr := s.getDocument(tx, existingID, actorID, false)
			if loadErr != nil {
				return loadErr
			}
			result = document
			return nil
		}
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
		publication := &model.DocumentPublication{DocumentID: id, VersionID: stringPointer(input.VersionID), VersionSequence: input.VersionSequence, Title: input.Title, Summary: input.Summary, Slug: input.Slug, Language: input.Language, Tags: tags, OwnerID: record.OwnerID, OwnerUsername: record.OwnerUsername, OwnerAvatar: record.OwnerAvatar, Content: content, PlainText: input.PlainText, PublishedAt: now, UpdatedAt: now}
		if err := tx.Save(publication).Error; err != nil {
			return mapWriteError("save publication snapshot", err)
		}
		if err := s.replaceDocumentTags(tx, record.OwnerID, id, input.Tags, now); err != nil {
			return err
		}
		var alias model.SlugAlias
		aliasErr := tx.Where("slug = ?", input.Slug).First(&alias).Error
		if aliasErr == nil && alias.DocumentID != nil && *alias.DocumentID != id {
			return ErrConflict
		}
		if errors.Is(aliasErr, gorm.ErrRecordNotFound) {
			if err := tx.Create(&model.SlugAlias{Slug: input.Slug, DocumentID: stringPointer(id), CreatedAt: now}).Error; err != nil {
				return mapWriteError("reserve publication slug", err)
			}
		} else if aliasErr != nil {
			return fmt.Errorf("check publication slug: %w", aliasErr)
		} else if err := tx.Model(&alias).Updates(map[string]any{"document_id": id, "gone_at": nil}).Error; err != nil {
			return fmt.Errorf("activate publication slug: %w", err)
		}
		if err := tx.Model(record).Updates(map[string]any{"published": true, "published_at": now, "metadata_revision": gorm.Expr("metadata_revision + 1"), "permission_revision": gorm.Expr("permission_revision + 1"), "updated_at": now}).Error; err != nil {
			return fmt.Errorf("mark document published: %w", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload published document: %w", err)
		}
		if err := s.enqueuePermissionChanged(tx, record, now); err != nil {
			return err
		}
		if err := s.saveIdempotency(tx, input.Idempotency, id); err != nil {
			return err
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		result.Tags = append([]string(nil), input.Tags...)
		return nil
	})
	return result, err
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
		Access              string     `gorm:"column:access"`
		ProjectedAt         *time.Time `gorm:"column:projected_at"`
		PublicationTitle    *string    `gorm:"column:publication_title"`
		PublicationSummary  *string    `gorm:"column:publication_summary"`
		PublicationSlug     *string    `gorm:"column:publication_slug"`
		PublicationLanguage *string    `gorm:"column:publication_language"`
		PublicationTags     []byte     `gorm:"column:publication_tags"`
	}
	selectSQL, args := buildListDocumentsQuery(options, limit)
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
		document := documentFromModel(&rows[index].Document, rows[index].Access, projection)
		if options.Published {
			if rows[index].PublicationTitle != nil {
				document.Title = *rows[index].PublicationTitle
			}
			if rows[index].PublicationSummary != nil {
				document.Summary = *rows[index].PublicationSummary
			}
			if rows[index].PublicationSlug != nil {
				document.Slug = *rows[index].PublicationSlug
			}
			if rows[index].PublicationLanguage != nil {
				document.Language = *rows[index].PublicationLanguage
			}
			document.Tags = publicationTags(rows[index].PublicationTags)
		}
		result = append(result, document)
	}
	return result, nil
}

func buildListDocumentsQuery(options ListOptions, limit int) (string, []any) {
	selectSQL := `SELECT d.*, p.projected_at, pub.title AS publication_title, pub.summary AS publication_summary, pub.slug AS publication_slug, pub.language AS publication_language, pub.tags AS publication_tags, CASE WHEN d.owner_id = ? THEN 'owner' ELSE COALESCE(m.role, 'none') END AS access
FROM knowledge.documents d
LEFT JOIN knowledge.document_members m ON m.document_id = d.id AND m.user_id = ?
LEFT JOIN knowledge.document_projections p ON p.document_id = d.id
LEFT JOIN knowledge.document_publications pub ON pub.document_id = d.id`
	args := []any{options.ActorID, options.ActorID}
	if options.Query != "" {
		selectSQL += `
JOIN (
  SELECT id AS document_id
  FROM knowledge.documents
  WHERE title ILIKE ? ESCAPE '\' OR summary ILIKE ? ESCAPE '\'
  UNION
  SELECT document_id
  FROM knowledge.document_projections
  WHERE plain_text ILIKE ? ESCAPE '\'
) matched ON matched.document_id = d.id`
		pattern := "%" + escapeLike(options.Query) + "%"
		args = append(args, pattern, pattern, pattern)
	}
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
	return selectSQL, args
}

func (s *Store) UpdateDocument(ctx context.Context, id string, actorID, expected int64, title, summary, slug, language *string, tags []string, folderID *string) (*domain.Document, error) {
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
		if language != nil {
			updates["language"] = *language
		}
		if slug != nil && *slug != record.Slug {
			var alias model.SlugAlias
			aliasErr := tx.Where("slug = ?", *slug).First(&alias).Error
			if aliasErr == nil && alias.DocumentID != nil && *alias.DocumentID != id {
				return ErrConflict
			}
			if errors.Is(aliasErr, gorm.ErrRecordNotFound) {
				if err := tx.Create(&model.SlugAlias{Slug: *slug, DocumentID: stringPointer(id), CreatedAt: now}).Error; err != nil {
					return mapWriteError("reserve updated document slug", err)
				}
			} else if aliasErr != nil {
				return fmt.Errorf("check updated document slug: %w", aliasErr)
			}
			updates["slug"] = *slug
		}
		if folderID != nil {
			if *folderID == "" {
				if err := tx.Where("owner_id = ? AND document_id = ?", record.OwnerID, id).Delete(&model.DocumentPlacement{}).Error; err != nil {
					return fmt.Errorf("clear document folder: %w", err)
				}
			} else {
				var folder model.Folder
				if err := tx.Where("id = ? AND owner_id = ?", *folderID, record.OwnerID).First(&folder).Error; err != nil {
					return mapNotFound("get document folder", err)
				}
				placement := &model.DocumentPlacement{OwnerID: record.OwnerID, DocumentID: id, FolderID: folderID, Revision: 1, CreatedAt: now, UpdatedAt: now}
				if err := tx.Where("owner_id = ? AND document_id = ?", record.OwnerID, id).Assign(map[string]any{"folder_id": *folderID, "updated_at": now, "revision": gorm.Expr("revision + 1")}).FirstOrCreate(placement).Error; err != nil {
					return mapWriteError("place document in folder", err)
				}
			}
		}
		if err := tx.Model(record).Updates(updates).Error; err != nil {
			return mapWriteError("update document", err)
		}
		if err := tx.Where("id = ?", id).First(record).Error; err != nil {
			return fmt.Errorf("reload updated document: %w", err)
		}
		if tags != nil {
			if err := s.replaceDocumentTags(tx, record.OwnerID, id, tags, now); err != nil {
				return err
			}
		}
		projection, err := getProjection(tx, id)
		if err != nil {
			return err
		}
		result = documentFromModel(record, access, projection)
		if tags != nil {
			result.Tags = append([]string(nil), tags...)
		}
		if folderID != nil {
			if *folderID == "" {
				result.FolderID = nil
			} else {
				result.FolderID = stringPointer(*folderID)
			}
		}
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

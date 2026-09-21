package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CommitInput struct {
	Kind        string
	Label       string
	Description string
	Content     *domain.RichTextDocument
	PlainText   string
	ContentHash string
	Idempotency Idempotency
}

func contentHash(content domain.RichTextDocument, plainText string) (string, error) {
	payload, err := jsoncodec.Marshal(struct {
		Content   domain.RichTextDocument `json:"content"`
		PlainText string                  `json:"plain_text"`
	}{content, plainText})
	if err != nil {
		return "", fmt.Errorf("encode content hash: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func commitFromModel(value *model.DocumentCommit) (*domain.Commit, error) {
	if value == nil {
		return nil, errors.New("commit is nil")
	}
	var content domain.RichTextDocument
	if err := jsoncodec.Unmarshal(value.Content, &content); err != nil {
		return nil, fmt.Errorf("decode commit content: %w", err)
	}
	return &domain.Commit{
		ID: value.ID, DocumentID: value.DocumentID, Kind: value.Kind, Label: value.Label,
		Description: value.Description, Contributor: value.ContributorName, Sequence: value.Sequence,
		ContentHash: value.ContentHash, Content: content, PlainText: value.PlainText,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}, nil
}

func (s *Store) CreateCommit(ctx context.Context, documentID string, actorID int64, input CommitInput) (*domain.Commit, error) {
	var result *domain.Commit
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, input.Idempotency)
		if err != nil {
			return err
		}
		if found {
			var existing model.DocumentCommit
			if err := tx.Where("id = ?", existingID).First(&existing).Error; err != nil {
				return mapNotFound("load idempotent commit", err)
			}
			result, err = commitFromModel(&existing)
			return err
		}
		record, access, err := lockDocument(tx, documentID, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		projection, err := getProjection(tx, documentID)
		if err != nil {
			return err
		}
		content := input.Content
		plainText := input.PlainText
		if content == nil {
			var current domain.RichTextDocument
			if err := jsoncodec.Unmarshal(projection.Content, &current); err != nil {
				return fmt.Errorf("decode current projection: %w", err)
			}
			content = &current
			plainText = projection.PlainText
		}
		if err := content.Validate(); err != nil {
			return err
		}
		if strings.TrimSpace(input.Kind) == "" {
			input.Kind = "manual"
		}
		if strings.TrimSpace(input.Label) == "" {
			input.Label = input.Kind
		}
		hash := strings.TrimSpace(input.ContentHash)
		if hash == "" {
			hash, err = contentHash(*content, plainText)
			if err != nil {
				return err
			}
		}
		encoded, err := jsoncodec.Marshal(content)
		if err != nil {
			return err
		}
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		commit := &model.DocumentCommit{ID: id, DocumentID: documentID, Kind: input.Kind, Label: input.Label,
			Description: input.Description, ContributorID: actorID, ContributorName: record.OwnerUsername,
			Sequence: projection.Sequence, ContentHash: hash, Content: encoded, PlainText: plainText,
			IsAnchor: true, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(commit).Error; err != nil {
			return mapWriteError("create document commit", err)
		}
		if err := s.saveIdempotency(tx, input.Idempotency, id); err != nil {
			return err
		}
		result, err = commitFromModel(commit)
		return err
	})
	return result, err
}

func (s *Store) ListCommits(ctx context.Context, documentID string, actorID int64, limit int) ([]*domain.Commit, error) {
	_, access, err := lockDocument(s.db.WithContext(ctx), documentID, actorID, false)
	if err != nil {
		return nil, err
	}
	if !domain.CanRead(access) {
		return nil, ErrForbidden
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var rows []model.DocumentCommit
	if err := s.db.WithContext(ctx).Where("document_id = ?", documentID).
		Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list document commits: %w", err)
	}
	result := make([]*domain.Commit, 0, len(rows))
	for index := range rows {
		value, err := commitFromModel(&rows[index])
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *Store) GetCommit(ctx context.Context, commitID string, actorID int64) (*domain.Commit, error) {
	var row model.DocumentCommit
	if err := s.db.WithContext(ctx).Where("id = ?", commitID).First(&row).Error; err != nil {
		return nil, mapNotFound("get document commit", err)
	}
	_, access, err := lockDocument(s.db.WithContext(ctx), row.DocumentID, actorID, false)
	if err != nil {
		return nil, err
	}
	if !domain.CanRead(access) {
		return nil, ErrForbidden
	}
	return commitFromModel(&row)
}

func (s *Store) RenameCommit(ctx context.Context, commitID string, actorID int64, label, description string) (*domain.Commit, error) {
	var result *domain.Commit
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.DocumentCommit
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", commitID).First(&row).Error; err != nil {
			return mapNotFound("rename document commit", err)
		}
		_, access, err := lockDocument(tx, row.DocumentID, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		if strings.TrimSpace(label) == "" || len([]rune(label)) > 160 || len([]rune(description)) > 2000 {
			return &domain.ValidationError{Field: "commit", Reason: "label and description are invalid"}
		}
		row.Label, row.Description, row.UpdatedAt = strings.TrimSpace(label), strings.TrimSpace(description), s.now().UTC()
		if err := tx.Save(&row).Error; err != nil {
			return mapWriteError("rename document commit", err)
		}
		result, err = commitFromModel(&row)
		return err
	})
	return result, err
}

func (s *Store) RestoreCommit(ctx context.Context, commitID string, actorID int64, idempotency Idempotency) (*domain.Document, error) {
	var result *domain.Document
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		existingID, found, err := s.idempotentResource(tx, idempotency)
		if err != nil {
			return err
		}
		if found {
			result, err = s.getDocument(tx, existingID, actorID, false)
			return err
		}
		var commit model.DocumentCommit
		if err := tx.Where("id = ?", commitID).First(&commit).Error; err != nil {
			return mapNotFound("restore document commit", err)
		}
		record, access, err := lockDocument(tx, commit.DocumentID, actorID, false)
		if err != nil {
			return err
		}
		if !domain.CanEdit(access) {
			return ErrForbidden
		}
		var projection model.Projection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("document_id = ?", commit.DocumentID).First(&projection).Error; err != nil {
			return mapNotFound("restore document projection", err)
		}
		now := s.now().UTC()
		if err := tx.Model(&projection).Updates(map[string]any{"content": commit.Content, "plain_text": commit.PlainText, "sequence": gorm.Expr("sequence + 1"), "projected_at": now}).Error; err != nil {
			return fmt.Errorf("restore document projection: %w", err)
		}
		if err := tx.Model(record).Updates(map[string]any{"content_revision": gorm.Expr("content_revision + 1"), "updated_at": now}).Error; err != nil {
			return fmt.Errorf("restore document revision: %w", err)
		}
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		restored := commit
		restored.ID, restored.Kind, restored.Label, restored.CreatedAt, restored.UpdatedAt = id, "restore", "Restored "+commit.Label, now, now
		if err := tx.Create(&restored).Error; err != nil {
			return mapWriteError("create restore commit", err)
		}
		if err := s.saveIdempotency(tx, idempotency, record.ID); err != nil {
			return err
		}
		result = documentFromModel(record, access, &projection)
		return nil
	})
	return result, err
}

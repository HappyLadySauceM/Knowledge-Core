package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	historyIdleWindow = 5 * time.Minute
	historyMaxWindow  = 30 * time.Minute
)

type HistoryCheckpoint struct {
	ID               string
	DocumentID       string
	Kind             string
	Sequence         int64
	MetadataRevision int64
	SemanticHash     string
	Content          []byte
	PlainText        string
	Metadata         []byte
	Contributors     []byte
	BlockDiff        []byte
	IsAnchor         bool
	CreatedAt        time.Time
}

func markHistoryDirty(tx *gorm.DB, documentID string, at time.Time) error {
	activity := &model.HistoryActivity{
		DocumentID: documentID, FirstDirtyAt: at, LastDirtyAt: at,
		Contributors: []byte("[]"), UpdatedAt: at,
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "document_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"last_dirty_at": at, "updated_at": at,
		}),
	}).Create(activity).Error
}

// CreateDueHistoryCheckpoint atomically snapshots the latest Knowledge projection.
// It returns nil when no document is due or when the semantic content is unchanged.
func (s *Store) CreateDueHistoryCheckpoint(ctx context.Context) (*HistoryCheckpoint, error) {
	var result *HistoryCheckpoint
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		var activity model.HistoryActivity
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("last_dirty_at <= ? OR first_dirty_at <= ?", now.Add(-historyIdleWindow), now.Add(-historyMaxWindow)).
			Order("first_dirty_at ASC").First(&activity).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return fmt.Errorf("claim due history activity: %w", err)
		}
		var document model.Document
		if err := tx.Where("id = ? AND deleted_at IS NULL", activity.DocumentID).First(&document).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return tx.Delete(&activity).Error
			}
			return fmt.Errorf("load history document: %w", err)
		}
		var projection model.Projection
		if err := tx.Where("document_id = ?", activity.DocumentID).First(&projection).Error; err != nil {
			return fmt.Errorf("load history projection: %w", err)
		}
		var tags []string
		if err := tx.Table("knowledge.tags").Select("knowledge.tags.name").
			Joins("JOIN knowledge.document_tags ON knowledge.document_tags.tag_id = knowledge.tags.id").
			Where("knowledge.document_tags.document_id = ?", activity.DocumentID).
			Order("knowledge.tags.name ASC").Pluck("knowledge.tags.name", &tags).Error; err != nil {
			return fmt.Errorf("load history tags: %w", err)
		}
		metadata, err := jsoncodec.Marshal(map[string]any{
			"title": document.Title, "summary": document.Summary, "language": document.Language,
			"tags": tags,
			"icon": document.Icon, "cover_attachment_id": document.CoverAttachmentID,
			"cover_focal_x": document.CoverFocalX, "cover_focal_y": document.CoverFocalY,
		})
		if err != nil {
			return fmt.Errorf("encode history metadata: %w", err)
		}
		var semanticContent any
		if err := jsoncodec.Unmarshal(projection.Content, &semanticContent); err != nil {
			return fmt.Errorf("decode history content: %w", err)
		}
		semanticContent = stripHistoryBlockIDs(semanticContent)
		canonicalContent, err := jsoncodec.Marshal(semanticContent)
		if err != nil {
			return fmt.Errorf("encode history semantic content: %w", err)
		}
		hash := sha256.Sum256(append(append([]byte{}, canonicalContent...), metadata...))
		semanticHash := hex.EncodeToString(hash[:])
		var existing int64
		if err := tx.Model(&model.DocumentHistory{}).Where("document_id = ? AND semantic_hash = ?", activity.DocumentID, semanticHash).Count(&existing).Error; err != nil {
			return fmt.Errorf("deduplicate history checkpoint: %w", err)
		}
		if existing == 0 {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			diff, err := latestBlockDiff(tx, activity.DocumentID, projection.Content)
			if err != nil {
				return err
			}
			row := &model.DocumentHistory{
				ID: id, DocumentID: activity.DocumentID, Kind: "automatic", Sequence: projection.Sequence,
				MetadataRevision: document.MetadataRevision, SemanticHash: semanticHash,
				Content: projection.Content, PlainText: projection.PlainText, Metadata: metadata,
				Contributors: activity.Contributors, BlockDiff: diff, IsAnchor: false, CreatedAt: now,
			}
			if err := tx.Create(row).Error; err != nil {
				return fmt.Errorf("create history checkpoint: %w", err)
			}
			result = historyFromModel(row)
		}
		return tx.Delete(&activity).Error
	})
	return result, err
}

func latestBlockDiff(tx *gorm.DB, documentID string, content []byte) ([]byte, error) {
	var previous model.DocumentHistory
	err := tx.Where("document_id = ?", documentID).Order("created_at DESC, id DESC").First(&previous).Error
	if err == gorm.ErrRecordNotFound {
		return []byte("[]"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("load previous history: %w", err)
	}
	var before, after struct {
		Content []map[string]any `json:"content"`
	}
	if json.Unmarshal(previous.Content, &before) != nil || json.Unmarshal(content, &after) != nil {
		return []byte("[]"), nil
	}
	beforeByID, afterByID := blocksByStableID(before.Content), blocksByStableID(after.Content)
	diff := make([]map[string]any, 0)
	beforeIDs := make([]string, 0, len(beforeByID))
	for blockID := range beforeByID {
		beforeIDs = append(beforeIDs, blockID)
	}
	sort.Strings(beforeIDs)
	for _, blockID := range beforeIDs {
		prior := beforeByID[blockID]
		current, exists := afterByID[blockID]
		if !exists {
			diff = append(diff, map[string]any{"block_id": blockID, "kind": "removed", "from": prior.index})
			continue
		}
		left, _ := json.Marshal(prior.value)
		right, _ := json.Marshal(current.value)
		if string(left) != string(right) {
			diff = append(diff, map[string]any{"block_id": blockID, "kind": "modified", "from": prior.index, "to": current.index})
		} else if prior.index != current.index {
			diff = append(diff, map[string]any{"block_id": blockID, "kind": "moved", "from": prior.index, "to": current.index})
		}
	}
	afterIDs := make([]string, 0, len(afterByID))
	for blockID := range afterByID {
		afterIDs = append(afterIDs, blockID)
	}
	sort.Strings(afterIDs)
	for _, blockID := range afterIDs {
		current := afterByID[blockID]
		if _, exists := beforeByID[blockID]; !exists {
			diff = append(diff, map[string]any{"block_id": blockID, "kind": "added", "to": current.index})
		}
	}
	return jsoncodec.Marshal(diff)
}

type indexedBlock struct {
	index int
	value map[string]any
}

func blocksByStableID(blocks []map[string]any) map[string]indexedBlock {
	result := make(map[string]indexedBlock, len(blocks))
	for index, block := range blocks {
		attrs, _ := block["attrs"].(map[string]any)
		blockID, _ := attrs["blockId"].(string)
		if blockID == "" {
			blockID, _ = attrs["block_id"].(string)
		}
		if blockID == "" {
			blockID = fmt.Sprintf("legacy:%d", index)
		}
		result[blockID] = indexedBlock{index: index, value: block}
	}
	return result
}

func historyFromModel(row *model.DocumentHistory) *HistoryCheckpoint {
	return &HistoryCheckpoint{ID: row.ID, DocumentID: row.DocumentID, Kind: row.Kind, Sequence: row.Sequence,
		MetadataRevision: row.MetadataRevision, SemanticHash: row.SemanticHash, Content: row.Content,
		PlainText: row.PlainText, Metadata: row.Metadata, Contributors: row.Contributors,
		BlockDiff: row.BlockDiff, IsAnchor: row.IsAnchor, CreatedAt: row.CreatedAt}
}

func (s *Store) ListHistory(ctx context.Context, documentID string, actorID int64, limit int) ([]*HistoryCheckpoint, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if _, err := s.getDocument(s.db.WithContext(ctx), documentID, actorID, false); err != nil {
		return nil, err
	}
	var rows []model.DocumentHistory
	if err := s.db.WithContext(ctx).Where("document_id = ?", documentID).
		Order("created_at DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list document history: %w", err)
	}
	result := make([]*HistoryCheckpoint, 0, len(rows))
	for index := range rows {
		result = append(result, historyFromModel(&rows[index]))
	}
	return result, nil
}

func (s *Store) GetHistory(ctx context.Context, documentID, revisionID string, actorID int64) (*HistoryCheckpoint, error) {
	if _, err := s.getDocument(s.db.WithContext(ctx), documentID, actorID, false); err != nil {
		return nil, err
	}
	var row model.DocumentHistory
	if err := s.db.WithContext(ctx).Where("id = ? AND document_id = ?", revisionID, documentID).First(&row).Error; err != nil {
		return nil, mapNotFound("get document history", err)
	}
	return historyFromModel(&row), nil
}

func (s *Store) PruneHistory(ctx context.Context) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`DELETE FROM knowledge.document_history WHERE is_anchor = false AND created_at < CURRENT_TIMESTAMP - INTERVAL '180 days'`).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM knowledge.document_history h USING (
            SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY document_id ORDER BY created_at DESC, id DESC) rank
            FROM knowledge.document_history WHERE is_anchor = false) ranked WHERE rank > 500
        ) stale WHERE h.id = stale.id`).Error
	})
}

func createHistoryAnchor(tx *gorm.DB, document *model.Document, projection *model.Projection, kind string, metadata map[string]any, now time.Time) error {
	metadataJSON, err := jsoncodec.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode %s history metadata: %w", kind, err)
	}
	var semanticContent any
	if err := jsoncodec.Unmarshal(projection.Content, &semanticContent); err != nil {
		return fmt.Errorf("decode %s history content: %w", kind, err)
	}
	canonicalContent, err := jsoncodec.Marshal(stripHistoryBlockIDs(semanticContent))
	if err != nil {
		return fmt.Errorf("encode %s history content: %w", kind, err)
	}
	digest := sha256.Sum256(append(append([]byte{}, canonicalContent...), metadataJSON...))
	semanticHash := hex.EncodeToString(digest[:])
	var existing model.DocumentHistory
	if err := tx.Where("document_id = ? AND semantic_hash = ?", document.ID, semanticHash).First(&existing).Error; err == nil {
		return tx.Model(&existing).Updates(map[string]any{"kind": kind, "is_anchor": true}).Error
	} else if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("load existing %s history anchor: %w", kind, err)
	}
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	diff, err := latestBlockDiff(tx, document.ID, projection.Content)
	if err != nil {
		return err
	}
	return tx.Create(&model.DocumentHistory{
		ID: id, DocumentID: document.ID, Kind: kind, Sequence: projection.Sequence,
		MetadataRevision: document.MetadataRevision, SemanticHash: semanticHash,
		Content: projection.Content, PlainText: projection.PlainText, Metadata: metadataJSON,
		Contributors: []byte("[]"), BlockDiff: diff, IsAnchor: true, CreatedAt: now,
	}).Error
}

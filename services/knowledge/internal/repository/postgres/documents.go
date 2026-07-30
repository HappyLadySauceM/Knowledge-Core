package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
	"github.com/jackc/pgx/v5/pgconn"
)

const documentColumns = `
    id, title, summary, COALESCE(slug, ''), status, author_id, current_version,
    published_at, created_at, updated_at`

type DocumentRepository struct{}

func NewDocumentRepository() *DocumentRepository { return &DocumentRepository{} }

func (r *DocumentRepository) Create(ctx context.Context, executor database.Executor, document *domain.Document) error {
	if executor == nil || document == nil {
		return errors.New("create knowledge document: executor and document are required")
	}
	err := executor.QueryRowContext(ctx, `
        INSERT INTO knowledge.documents (title, summary, status, author_id)
        VALUES ($1, $2, $3, $4)
        RETURNING id, current_version, published_at, created_at, updated_at`,
		document.Title, document.Summary, document.Status, document.AuthorID,
	).Scan(&document.ID, &document.CurrentVersion, &document.PublishedAt, &document.CreatedAt, &document.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert knowledge document: %w", err)
	}
	document.Slug = fmt.Sprintf("document-%d", document.ID)
	if _, err := executor.ExecContext(ctx, `UPDATE knowledge.documents SET slug = $2 WHERE id = $1`, document.ID, document.Slug); err != nil {
		return fmt.Errorf("set knowledge document slug: %w", err)
	}
	return nil
}

func (r *DocumentRepository) FindByID(ctx context.Context, executor database.Executor, id int64) (*domain.Document, error) {
	return scanDocument(executor.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM knowledge.documents WHERE id = $1`, id))
}

func (r *DocumentRepository) FindByIDForUpdate(ctx context.Context, executor database.Executor, id int64) (*domain.Document, error) {
	return scanDocument(executor.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM knowledge.documents WHERE id = $1 FOR UPDATE`, id))
}

func (r *DocumentRepository) FindPublishedByID(ctx context.Context, executor database.Executor, id int64) (*repository.PublishedDocument, error) {
	row := executor.QueryRowContext(ctx, `
		SELECT d.id, r.title, r.summary, COALESCE(d.slug, ''), d.status, d.author_id,
		       r.version, d.published_at, d.created_at, r.created_at, r.content_json::text
        FROM knowledge.documents d
		JOIN knowledge.document_revisions r
		  ON r.id = d.published_revision_id AND r.document_id = d.id
        WHERE d.id = $1 AND d.status = 'published'`, id)
	document := &domain.Document{}
	var contentJSON []byte
	err := row.Scan(
		&document.ID,
		&document.Title,
		&document.Summary,
		&document.Slug,
		&document.Status,
		&document.AuthorID,
		&document.CurrentVersion,
		&document.PublishedAt,
		&document.CreatedAt,
		&document.UpdatedAt,
		&contentJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("select published knowledge document: %w", err)
	}
	return &repository.PublishedDocument{Document: document, ContentJSON: contentJSON}, nil
}

func (r *DocumentRepository) List(ctx context.Context, executor database.Executor, query string, page, pageSize int, publishedOnly bool) (domain.List, error) {
	where := `WHERE ($1 = '' OR d.search_vector @@ websearch_to_tsquery('simple', $1))`
	join := ""
	orderBy := "d.updated_at DESC, d.id DESC"
	selectColumns := `d.id, d.title, d.summary, COALESCE(d.slug, ''), d.status, d.author_id,
        d.current_version, d.published_at, d.created_at, d.updated_at`
	if publishedOnly {
		join = `JOIN knowledge.document_revisions r
			ON r.id = d.published_revision_id AND r.document_id = d.id`
		where = `WHERE d.status = 'published' AND (
			$1 = '' OR r.search_vector @@ websearch_to_tsquery('simple', $1)
		)`
		selectColumns = `d.id, r.title, r.summary, COALESCE(d.slug, ''), d.status, d.author_id,
			r.version, d.published_at, d.created_at, r.created_at`
		orderBy = "d.published_at DESC, d.id DESC"
	}
	var total int64
	if err := executor.QueryRowContext(ctx, `SELECT count(*) FROM knowledge.documents d `+join+` `+where, query).Scan(&total); err != nil {
		return domain.List{}, fmt.Errorf("count knowledge documents: %w", err)
	}
	rows, err := executor.QueryContext(ctx, `SELECT `+selectColumns+` FROM knowledge.documents d `+join+` `+where+`
		ORDER BY `+orderBy+` LIMIT $2 OFFSET $3`, query, pageSize, (page-1)*pageSize)
	if err != nil {
		return domain.List{}, fmt.Errorf("list knowledge documents: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := domain.List{Items: make([]*domain.Document, 0), Total: total, Page: page, PageSize: pageSize}
	for rows.Next() {
		document, scanErr := scanDocument(rows)
		if scanErr != nil {
			return domain.List{}, scanErr
		}
		result.Items = append(result.Items, document)
	}
	if err := rows.Err(); err != nil {
		return domain.List{}, fmt.Errorf("iterate knowledge documents: %w", err)
	}
	return result, nil
}

func (r *DocumentRepository) ListBlocks(ctx context.Context, executor database.Executor, documentID int64) ([]*domain.Block, error) {
	rows, err := executor.QueryContext(ctx, `
        SELECT block_id, document_id, position_key, type, content_json::text, text_content, version, updated_by, updated_at
        FROM knowledge.document_blocks WHERE document_id = $1 ORDER BY position_key, block_id`, documentID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge document blocks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	blocks := make([]*domain.Block, 0)
	for rows.Next() {
		block, scanErr := scanBlock(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		blocks = append(blocks, block)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge document blocks: %w", err)
	}
	return blocks, nil
}

func (r *DocumentRepository) FindBlock(ctx context.Context, executor database.Executor, documentID int64, blockID string) (*domain.Block, error) {
	return scanBlock(executor.QueryRowContext(ctx, `
        SELECT block_id, document_id, position_key, type, content_json::text, text_content, version, updated_by, updated_at
        FROM knowledge.document_blocks WHERE document_id = $1 AND block_id = $2`, documentID, blockID))
}

func (r *DocumentRepository) UpdateMetadata(ctx context.Context, executor database.Executor, document *domain.Document) error {
	if executor == nil || document == nil {
		return errors.New("update knowledge document metadata: executor and document are required")
	}
	updated, err := scanDocument(executor.QueryRowContext(ctx, `
        UPDATE knowledge.documents
        SET title = $2, summary = $3, current_version = current_version + 1, updated_at = CURRENT_TIMESTAMP
        WHERE id = $1
        RETURNING `+documentColumns, document.ID, document.Title, document.Summary))
	if err != nil {
		return err
	}
	*document = *updated
	return nil
}

func (r *DocumentRepository) Delete(ctx context.Context, executor database.Executor, id int64) error {
	result, err := executor.ExecContext(ctx, `DELETE FROM knowledge.documents WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete knowledge document: %w", err)
	}
	return requireRows(result, repository.ErrDocumentNotFound, "delete knowledge document")
}

func (r *DocumentRepository) SetStatus(
	ctx context.Context,
	executor database.Executor,
	document *domain.Document,
	status string,
	publishedRevisionID *int64,
) error {
	if executor == nil || document == nil {
		return errors.New("set knowledge document status: executor and document are required")
	}
	updated, err := scanDocument(executor.QueryRowContext(ctx, `
        UPDATE knowledge.documents
        SET status = $2,
			published_revision_id = $3,
			published_at = CASE WHEN $2 = 'published' THEN CURRENT_TIMESTAMP ELSE NULL END,
			updated_at = CURRENT_TIMESTAMP
        WHERE id = $1
		RETURNING `+documentColumns, document.ID, status, publishedRevisionID))
	if err != nil {
		return err
	}
	*document = *updated
	return nil
}

func (r *DocumentRepository) LockOperation(ctx context.Context, executor database.Executor, operationID string) error {
	if executor == nil || operationID == "" {
		return errors.New("lock knowledge document operation: executor and operation ID are required")
	}
	if _, err := executor.ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(hashtextextended('knowledge.document_ops:' || $1, 0))`, operationID); err != nil {
		return fmt.Errorf("lock knowledge document operation: %w", err)
	}
	return nil
}

func (r *DocumentRepository) FindOperation(ctx context.Context, executor database.Executor, operationID string) (repository.StoredOperation, error) {
	stored := repository.StoredOperation{
		Operation: domain.Operation{OperationID: operationID},
		Ack:       domain.OperationAck{OperationID: operationID, Duplicate: true},
	}
	err := executor.QueryRowContext(ctx, `
		SELECT document_id, actor_id, base_document_version, base_block_version, block_id, op_type,
		       payload_json->>'position_key', payload_json->>'content_json', payload_json->>'text_content',
		       document_version, block_version
		FROM knowledge.document_ops WHERE op_id = $1`, operationID).Scan(
		&stored.Operation.DocumentID,
		&stored.Operation.ActorID,
		&stored.Operation.BaseDocumentVersion,
		&stored.Operation.BaseBlockVersion,
		&stored.Operation.BlockID,
		&stored.Type,
		&stored.Operation.PositionKey,
		&stored.Operation.ContentJSON,
		&stored.Operation.TextContent,
		&stored.Ack.DocumentVersion,
		&stored.Ack.BlockVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return repository.StoredOperation{}, repository.ErrOperationNotFound
	}
	if err != nil {
		return repository.StoredOperation{}, fmt.Errorf("find knowledge document operation: %w", err)
	}
	stored.Ack.DocumentID = stored.Operation.DocumentID
	return stored, nil
}

func (r *DocumentRepository) SaveBlock(ctx context.Context, executor database.Executor, block *domain.Block) error {
	if executor == nil || block == nil {
		return errors.New("save knowledge document block: executor and block are required")
	}
	result, err := executor.ExecContext(ctx, `
        INSERT INTO knowledge.document_blocks (
            block_id, document_id, position_key, type, content_json, text_content, version, updated_by, updated_at
        ) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, CURRENT_TIMESTAMP)
        ON CONFLICT (block_id) DO UPDATE SET
            position_key = EXCLUDED.position_key,
            type = EXCLUDED.type,
            content_json = EXCLUDED.content_json,
            text_content = EXCLUDED.text_content,
            version = EXCLUDED.version,
            updated_by = EXCLUDED.updated_by,
            updated_at = CURRENT_TIMESTAMP
        WHERE knowledge.document_blocks.document_id = EXCLUDED.document_id`,
		block.BlockID, block.DocumentID, block.PositionKey, block.Type, block.ContentJSON, block.TextContent, block.Version, block.UpdatedBy)
	if err != nil {
		return mapJSONError("save knowledge document block", err)
	}
	if err := requireRows(result, repository.ErrBlockConflict, "save knowledge document block"); err != nil {
		return err
	}
	block.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *DocumentRepository) IncrementVersion(ctx context.Context, executor database.Executor, document *domain.Document) error {
	if executor == nil || document == nil {
		return errors.New("increment knowledge document version: executor and document are required")
	}
	updated, err := scanDocument(executor.QueryRowContext(ctx, `
        UPDATE knowledge.documents SET current_version = current_version + 1, updated_at = CURRENT_TIMESTAMP
        WHERE id = $1 RETURNING `+documentColumns, document.ID))
	if err != nil {
		return err
	}
	*document = *updated
	return nil
}

func (r *DocumentRepository) SaveOperation(
	ctx context.Context,
	executor database.Executor,
	operation domain.Operation,
	ack domain.OperationAck,
	payloadJSON []byte,
) error {
	_, err := executor.ExecContext(ctx, `
		INSERT INTO knowledge.document_ops (
			op_id, document_id, actor_id, base_document_version, base_block_version, block_id, op_type,
			payload_json, document_version, block_version
		) VALUES ($1, $2, $3, $4, $5, $6, 'upsert_block', $7::jsonb, $8, $9)`,
		operation.OperationID, operation.DocumentID, operation.ActorID, operation.BaseDocumentVersion,
		operation.BaseBlockVersion, operation.BlockID, payloadJSON, ack.DocumentVersion, ack.BlockVersion)
	if err != nil {
		return mapJSONError("save knowledge document operation", err)
	}
	return nil
}

func (r *DocumentRepository) SaveRevision(
	ctx context.Context,
	executor database.Executor,
	document *domain.Document,
	actorID int64,
	contentJSON []byte,
) (int64, error) {
	if executor == nil || document == nil {
		return 0, errors.New("save knowledge document revision: executor and document are required")
	}
	var revisionID int64
	err := executor.QueryRowContext(ctx, `
		INSERT INTO knowledge.document_revisions (document_id, version, title, summary, content_json, published_by)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		ON CONFLICT (document_id, version) DO UPDATE SET version = EXCLUDED.version
		RETURNING id`,
		document.ID, document.CurrentVersion, document.Title, document.Summary, contentJSON, actorID).Scan(&revisionID)
	if err != nil {
		return 0, mapJSONError("save knowledge document revision", err)
	}
	return revisionID, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDocument(row rowScanner) (*domain.Document, error) {
	document := &domain.Document{}
	err := row.Scan(
		&document.ID,
		&document.Title,
		&document.Summary,
		&document.Slug,
		&document.Status,
		&document.AuthorID,
		&document.CurrentVersion,
		&document.PublishedAt,
		&document.CreatedAt,
		&document.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan knowledge document: %w", err)
	}
	return document, nil
}

func scanBlock(row rowScanner) (*domain.Block, error) {
	block := &domain.Block{}
	err := row.Scan(
		&block.BlockID,
		&block.DocumentID,
		&block.PositionKey,
		&block.Type,
		&block.ContentJSON,
		&block.TextContent,
		&block.Version,
		&block.UpdatedBy,
		&block.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrBlockNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan knowledge document block: %w", err)
	}
	return block, nil
}

func requireRows(result sql.Result, absent error, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s result: %w", operation, err)
	}
	if rows == 0 {
		return absent
	}
	return nil
}

func mapJSONError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "22P02" {
		return repository.ErrInvalidJSON
	}
	if strings.Contains(err.Error(), "invalid input syntax for type json") {
		return repository.ErrInvalidJSON
	}
	return fmt.Errorf("%s: %w", operation, err)
}

var _ repository.DocumentRepository = (*DocumentRepository)(nil)

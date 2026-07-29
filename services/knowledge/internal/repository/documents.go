package repository

import (
	"context"
	"errors"

	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
)

var (
	ErrDocumentNotFound  = errors.New("knowledge repository: document not found")
	ErrBlockNotFound     = errors.New("knowledge repository: block not found")
	ErrBlockConflict     = errors.New("knowledge repository: block conflict")
	ErrOperationNotFound = errors.New("knowledge repository: operation not found")
	ErrInvalidJSON       = errors.New("knowledge repository: invalid JSON")
)

type DocumentRepository interface {
	Create(ctx context.Context, executor database.Executor, document *domain.Document) error
	FindByID(ctx context.Context, executor database.Executor, id int64) (*domain.Document, error)
	FindByIDForUpdate(ctx context.Context, executor database.Executor, id int64) (*domain.Document, error)
	FindPublishedByID(ctx context.Context, executor database.Executor, id int64) (*PublishedDocument, error)
	List(ctx context.Context, executor database.Executor, query string, page, pageSize int, publishedOnly bool) (domain.List, error)
	ListBlocks(ctx context.Context, executor database.Executor, documentID int64) ([]*domain.Block, error)
	FindBlock(ctx context.Context, executor database.Executor, documentID int64, blockID string) (*domain.Block, error)
	UpdateMetadata(ctx context.Context, executor database.Executor, document *domain.Document) error
	Delete(ctx context.Context, executor database.Executor, id int64) error
	SetStatus(ctx context.Context, executor database.Executor, document *domain.Document, status string) error
	FindOperation(ctx context.Context, executor database.Executor, operationID string) (domain.OperationAck, error)
	SaveBlock(ctx context.Context, executor database.Executor, block *domain.Block) error
	IncrementVersion(ctx context.Context, executor database.Executor, document *domain.Document) error
	SaveOperation(ctx context.Context, executor database.Executor, operation domain.Operation, ack domain.OperationAck, payloadJSON []byte) error
	SaveRevision(ctx context.Context, executor database.Executor, document *domain.Document, actorID int64, contentJSON []byte) error
}

type PublishedDocument struct {
	Document    *domain.Document
	ContentJSON []byte
}

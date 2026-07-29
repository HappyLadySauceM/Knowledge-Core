package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/internal/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
)

const (
	defaultPageSize = 20
	maximumPageSize = 100
)

var (
	ErrDocumentNotFound = errors.New("knowledge: document not found")
	ErrVersionConflict  = errors.New("knowledge: document version conflict")
)

type CreateInput struct {
	Title    string
	Summary  string
	AuthorID int64
}

type UpdateInput struct {
	DocumentID int64
	Title      string
	Summary    string
}

type ListInput struct {
	Query    string
	Page     int
	PageSize int
}

type Service struct {
	database  database.DB
	documents repository.DocumentRepository
	jsonCodec json.Codec
}

func NewService(database database.DB, documents repository.DocumentRepository, jsonCodec json.Codec) (*Service, error) {
	if database == nil || documents == nil || jsonCodec == nil {
		return nil, errors.New("create knowledge service: database, document repository, and JSON codec are required")
	}
	return &Service{database: database, documents: documents, jsonCodec: jsonCodec}, nil
}

func (s *Service) ListPublished(ctx context.Context, input ListInput) (domain.List, error) {
	input, err := normalizeListInput(input)
	if err != nil {
		return domain.List{}, err
	}
	result, err := s.documents.List(ctx, s.database, input.Query, input.Page, input.PageSize, true)
	if err != nil {
		return domain.List{}, fmt.Errorf("list published documents: %w", err)
	}
	return result, nil
}

func (s *Service) List(ctx context.Context, input ListInput) (domain.List, error) {
	input, err := normalizeListInput(input)
	if err != nil {
		return domain.List{}, err
	}
	result, err := s.documents.List(ctx, s.database, input.Query, input.Page, input.PageSize, false)
	if err != nil {
		return domain.List{}, fmt.Errorf("list documents: %w", err)
	}
	return result, nil
}

func (s *Service) GetPublished(ctx context.Context, documentID int64) (*domain.Detail, error) {
	if documentID <= 0 {
		return nil, &domain.ValidationError{Field: "document_id", Reason: "must be positive"}
	}
	published, err := s.documents.FindPublishedByID(ctx, s.database, documentID)
	if errors.Is(err, repository.ErrDocumentNotFound) {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get published document: %w", err)
	}
	blocks := make([]*domain.Block, 0)
	if err := s.jsonCodec.Unmarshal(published.ContentJSON, &blocks); err != nil {
		return nil, fmt.Errorf("decode published document revision: %w", err)
	}
	return &domain.Detail{Document: published.Document, Blocks: blocks}, nil
}

func (s *Service) Create(ctx context.Context, input CreateInput) (*domain.Detail, error) {
	document, err := domain.NewDocument(input.Title, input.Summary, input.AuthorID)
	if err != nil {
		return nil, err
	}
	if err := s.withTransaction(ctx, func(transaction database.Tx) error {
		if err := s.documents.Create(ctx, transaction, document); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("create document: %w", err)
	}
	return &domain.Detail{Document: document, Blocks: make([]*domain.Block, 0)}, nil
}

func (s *Service) Get(ctx context.Context, documentID int64) (*domain.Detail, error) {
	if documentID <= 0 {
		return nil, &domain.ValidationError{Field: "document_id", Reason: "must be positive"}
	}
	document, err := s.documents.FindByID(ctx, s.database, documentID)
	if errors.Is(err, repository.ErrDocumentNotFound) {
		return nil, ErrDocumentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}
	blocks, err := s.documents.ListBlocks(ctx, s.database, documentID)
	if err != nil {
		return nil, fmt.Errorf("get document blocks: %w", err)
	}
	return &domain.Detail{Document: document, Blocks: blocks}, nil
}

func (s *Service) Update(ctx context.Context, input UpdateInput) (*domain.Detail, error) {
	if input.DocumentID <= 0 {
		return nil, &domain.ValidationError{Field: "document_id", Reason: "must be positive"}
	}
	title, summary, err := domain.NormalizeMetadata(input.Title, input.Summary)
	if err != nil {
		return nil, err
	}
	var document *domain.Document
	var blocks []*domain.Block
	if err := s.withTransaction(ctx, func(transaction database.Tx) error {
		document, err = s.documents.FindByIDForUpdate(ctx, transaction, input.DocumentID)
		if err != nil {
			return err
		}
		document.Title = title
		document.Summary = summary
		if err := s.documents.UpdateMetadata(ctx, transaction, document); err != nil {
			return err
		}
		blocks, err = s.documents.ListBlocks(ctx, transaction, input.DocumentID)
		return err
	}); err != nil {
		return nil, mapDocumentError("update document", err)
	}
	return &domain.Detail{Document: document, Blocks: blocks}, nil
}

func (s *Service) Delete(ctx context.Context, documentID int64) (*domain.Document, error) {
	if documentID <= 0 {
		return nil, &domain.ValidationError{Field: "document_id", Reason: "must be positive"}
	}
	var document *domain.Document
	if err := s.withTransaction(ctx, func(transaction database.Tx) error {
		var err error
		document, err = s.documents.FindByIDForUpdate(ctx, transaction, documentID)
		if err != nil {
			return err
		}
		return s.documents.Delete(ctx, transaction, documentID)
	}); err != nil {
		return nil, mapDocumentError("delete document", err)
	}
	return document, nil
}

func (s *Service) SetStatus(ctx context.Context, documentID int64, status string, actorID int64) (*domain.Document, error) {
	if documentID <= 0 {
		return nil, &domain.ValidationError{Field: "document_id", Reason: "must be positive"}
	}
	if actorID <= 0 {
		return nil, &domain.ValidationError{Field: "actor_id", Reason: "must be positive"}
	}
	if err := domain.ValidateStatus(status); err != nil {
		return nil, err
	}
	var document *domain.Document
	if err := s.withTransaction(ctx, func(transaction database.Tx) error {
		var err error
		document, err = s.documents.FindByIDForUpdate(ctx, transaction, documentID)
		if err != nil {
			return err
		}
		if status == domain.StatusPublished {
			blocks, listErr := s.documents.ListBlocks(ctx, transaction, documentID)
			if listErr != nil {
				return listErr
			}
			contentJSON, marshalErr := s.jsonCodec.Marshal(blocks)
			if marshalErr != nil {
				return fmt.Errorf("encode document revision: %w", marshalErr)
			}
			if err := s.documents.SaveRevision(ctx, transaction, document, actorID, contentJSON); err != nil {
				return err
			}
		}
		return s.documents.SetStatus(ctx, transaction, document, status)
	}); err != nil {
		return nil, mapDocumentError("set document status", err)
	}
	return document, nil
}

func (s *Service) ApplyOperation(ctx context.Context, operation domain.Operation) (domain.OperationAck, error) {
	if err := operation.Validate(); err != nil {
		return domain.OperationAck{}, err
	}
	if ack, err := s.documents.FindOperation(ctx, s.database, operation.OperationID); err == nil {
		if ack.DocumentID != operation.DocumentID {
			return domain.OperationAck{}, ErrVersionConflict
		}
		return ack, nil
	} else if !errors.Is(err, repository.ErrOperationNotFound) {
		return domain.OperationAck{}, fmt.Errorf("find existing document operation: %w", err)
	}

	var ack domain.OperationAck
	if err := s.withTransaction(ctx, func(transaction database.Tx) error {
		var err error
		if ack, err = s.documents.FindOperation(ctx, transaction, operation.OperationID); err == nil {
			if ack.DocumentID != operation.DocumentID {
				return ErrVersionConflict
			}
			return nil
		} else if !errors.Is(err, repository.ErrOperationNotFound) {
			return err
		}

		document, err := s.documents.FindByIDForUpdate(ctx, transaction, operation.DocumentID)
		if err != nil {
			return err
		}
		if document.CurrentVersion != operation.BaseDocumentVersion {
			return ErrVersionConflict
		}
		block, err := s.documents.FindBlock(ctx, transaction, operation.DocumentID, operation.BlockID)
		if errors.Is(err, repository.ErrBlockNotFound) {
			if operation.BaseBlockVersion != 0 {
				return ErrVersionConflict
			}
			block = &domain.Block{BlockID: operation.BlockID, DocumentID: operation.DocumentID, Type: "paragraph", Version: 1}
		} else if err != nil {
			return err
		} else if block.Version != operation.BaseBlockVersion {
			return ErrVersionConflict
		} else {
			block.Version++
		}
		block.PositionKey = strings.TrimSpace(operation.PositionKey)
		block.ContentJSON = operation.ContentJSON
		block.TextContent = operation.TextContent
		block.UpdatedBy = operation.ActorID
		if err := s.documents.SaveBlock(ctx, transaction, block); err != nil {
			return err
		}
		if err := s.documents.IncrementVersion(ctx, transaction, document); err != nil {
			return err
		}
		ack = domain.OperationAck{
			DocumentID:      operation.DocumentID,
			OperationID:     operation.OperationID,
			DocumentVersion: document.CurrentVersion,
			BlockVersion:    block.Version,
		}
		payloadJSON, err := s.jsonCodec.Marshal(struct {
			PositionKey string `json:"position_key"`
			ContentJSON string `json:"content_json"`
			TextContent string `json:"text_content"`
		}{block.PositionKey, block.ContentJSON, block.TextContent})
		if err != nil {
			return fmt.Errorf("encode document operation payload: %w", err)
		}
		return s.documents.SaveOperation(ctx, transaction, operation, ack, payloadJSON)
	}); err != nil {
		return domain.OperationAck{}, mapDocumentError("apply document operation", err)
	}
	return ack, nil
}

func (s *Service) withTransaction(ctx context.Context, fn func(database.Tx) error) (runErr error) {
	transaction, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge transaction: %w", err)
	}
	defer func() {
		if runErr != nil {
			runErr = errors.Join(runErr, transaction.Rollback())
		}
	}()
	if err := fn(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit knowledge transaction: %w", err)
	}
	return nil
}

func normalizeListInput(input ListInput) (ListInput, error) {
	input.Query = strings.TrimSpace(input.Query)
	if len([]rune(input.Query)) > 256 {
		return ListInput{}, &domain.ValidationError{Field: "query", Reason: "must not exceed 256 characters"}
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PageSize == 0 {
		input.PageSize = defaultPageSize
	}
	if input.Page < 1 {
		return ListInput{}, &domain.ValidationError{Field: "page", Reason: "must be positive"}
	}
	if input.PageSize < 1 || input.PageSize > maximumPageSize {
		return ListInput{}, &domain.ValidationError{Field: "page_size", Reason: "must be between 1 and 100"}
	}
	return input, nil
}

func mapDocumentError(operation string, err error) error {
	switch {
	case errors.Is(err, repository.ErrDocumentNotFound):
		return ErrDocumentNotFound
	case errors.Is(err, repository.ErrBlockConflict):
		return ErrVersionConflict
	case errors.Is(err, repository.ErrInvalidJSON):
		return &domain.ValidationError{Field: "content_json", Reason: "must contain valid JSON"}
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}

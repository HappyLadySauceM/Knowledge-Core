package kitex

import (
	"context"
	"errors"
	"time"

	auth "github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgerpc "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	knowledgeapp "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/klog"
)

const (
	CodeInvalidInput = knowledgerpc.CodeInvalidInput
	CodeNotFound     = knowledgerpc.CodeNotFound
	CodeConflict     = knowledgerpc.CodeConflict
	CodeForbidden    = knowledgerpc.CodeForbidden
	CodeInternal     = knowledgerpc.CodeInternal
)

type Application interface {
	ListPublished(ctx context.Context, input knowledgeapp.ListInput) (domain.List, error)
	List(ctx context.Context, input knowledgeapp.ListInput) (domain.List, error)
	GetPublished(ctx context.Context, documentID int64) (*domain.Detail, error)
	Create(ctx context.Context, input knowledgeapp.CreateInput) (*domain.Detail, error)
	Get(ctx context.Context, documentID int64) (*domain.Detail, error)
	Update(ctx context.Context, input knowledgeapp.UpdateInput) (*domain.Detail, error)
	Delete(ctx context.Context, documentID int64) (*domain.Document, error)
	SetStatus(ctx context.Context, documentID int64, status string, actorID int64) (*domain.Document, error)
	ApplyOperation(ctx context.Context, operation domain.Operation) (domain.OperationAck, error)
}

type TokenVerifier interface {
	Verify(value string) (auth.Principal, error)
}

type Handler struct {
	application Application
	verifier    TokenVerifier
}

func NewHandler(application Application, verifier TokenVerifier) *Handler {
	return &Handler{application: application, verifier: verifier}
}

func (h *Handler) Ping(context.Context, *common.PingRequest) (*common.PingResponse, error) {
	return &common.PingResponse{
		Service:  "knowledge",
		Status:   "ok",
		UnixTime: time.Now().UTC().Unix(),
	}, nil
}

func (h *Handler) ListPublishedDocuments(ctx context.Context, request *knowledgerpc.DocumentListRequest) (*knowledgerpc.DocumentList, error) {
	if request == nil {
		return nil, invalidRequest("invalid document list request")
	}
	if h.application == nil {
		return nil, internalError(ctx, errors.New("knowledge application is not configured"))
	}
	result, err := h.application.ListPublished(ctx, mapListInput(request))
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapList(result), nil
}

func (h *Handler) ListDocuments(ctx context.Context, request *knowledgerpc.DocumentListRequest) (*knowledgerpc.DocumentList, error) {
	if request == nil {
		return nil, invalidRequest("invalid document list request")
	}
	if _, err := h.authorizeAdmin(ctx); err != nil {
		return nil, err
	}
	result, err := h.application.List(ctx, mapListInput(request))
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapList(result), nil
}

func (h *Handler) GetPublishedDocument(ctx context.Context, request *knowledgerpc.DocumentIDRequest) (*knowledgerpc.DocumentDetail, error) {
	if request == nil {
		return nil, invalidRequest("invalid document request")
	}
	if h.application == nil {
		return nil, internalError(ctx, errors.New("knowledge application is not configured"))
	}
	detail, err := h.application.GetPublished(ctx, request.DocumentId)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapDetail(detail), nil
}

func (h *Handler) CreateDocument(ctx context.Context, request *knowledgerpc.CreateDocumentRequest) (*knowledgerpc.DocumentDetail, error) {
	if request == nil {
		return nil, invalidRequest("invalid create document request")
	}
	principal, err := h.authorizeAdmin(ctx)
	if err != nil {
		return nil, err
	}
	detail, err := h.application.Create(ctx, knowledgeapp.CreateInput{
		Title: request.Title, Summary: request.GetSummary(), AuthorID: principal.UserID,
	})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapDetail(detail), nil
}

func (h *Handler) GetDocument(ctx context.Context, request *knowledgerpc.DocumentIDRequest) (*knowledgerpc.DocumentDetail, error) {
	if request == nil {
		return nil, invalidRequest("invalid document request")
	}
	if _, err := h.authorizeAdmin(ctx); err != nil {
		return nil, err
	}
	detail, err := h.application.Get(ctx, request.DocumentId)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapDetail(detail), nil
}

func (h *Handler) UpdateDocument(ctx context.Context, request *knowledgerpc.UpdateDocumentRequest) (*knowledgerpc.DocumentDetail, error) {
	if request == nil {
		return nil, invalidRequest("invalid update document request")
	}
	if _, err := h.authorizeAdmin(ctx); err != nil {
		return nil, err
	}
	detail, err := h.application.Update(ctx, knowledgeapp.UpdateInput{
		DocumentID: request.DocumentId,
		Title:      request.Title,
		Summary:    request.Summary,
	})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapDetail(detail), nil
}

func (h *Handler) DeleteDocument(ctx context.Context, request *knowledgerpc.DocumentIDRequest) (*knowledgerpc.Document, error) {
	if request == nil {
		return nil, invalidRequest("invalid document request")
	}
	if _, err := h.authorizeAdmin(ctx); err != nil {
		return nil, err
	}
	document, err := h.application.Delete(ctx, request.DocumentId)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapDocument(document), nil
}

func (h *Handler) SetDocumentStatus(ctx context.Context, request *knowledgerpc.SetDocumentStatusRequest) (*knowledgerpc.Document, error) {
	if request == nil {
		return nil, invalidRequest("invalid document status request")
	}
	principal, err := h.authorizeAdmin(ctx)
	if err != nil {
		return nil, err
	}
	document, err := h.application.SetStatus(ctx, request.DocumentId, request.Status, principal.UserID)
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return mapDocument(document), nil
}

func (h *Handler) ApplyDocumentOperation(ctx context.Context, request *knowledgerpc.ApplyDocumentOperationRequest) (*knowledgerpc.DocumentOperationAck, error) {
	if request == nil {
		return nil, invalidRequest("invalid document operation request")
	}
	principal, err := h.authorizeAdmin(ctx)
	if err != nil {
		return nil, err
	}
	ack, err := h.application.ApplyOperation(ctx, domain.Operation{
		DocumentID:          request.DocumentId,
		OperationID:         request.OpId,
		BaseDocumentVersion: request.BaseDocumentVersion,
		BlockID:             request.BlockId,
		BaseBlockVersion:    request.BaseBlockVersion,
		PositionKey:         request.PositionKey,
		ContentJSON:         request.ContentJson,
		TextContent:         request.TextContent,
		ActorID:             principal.UserID,
	})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	return &knowledgerpc.DocumentOperationAck{
		DocumentId:      ack.DocumentID,
		OpId:            ack.OperationID,
		DocumentVersion: ack.DocumentVersion,
		BlockVersion:    ack.BlockVersion,
		Duplicate:       ack.Duplicate,
	}, nil
}

func (h *Handler) authorizeAdmin(ctx context.Context) (auth.Principal, error) {
	if h.application == nil || h.verifier == nil {
		return auth.Principal{}, internalError(ctx, errors.New("knowledge authorization is not configured"))
	}
	principal, err := h.verifier.Verify(auth.AccessToken(ctx))
	if err != nil || principal.Role != "admin" {
		return auth.Principal{}, kerrors.NewBizStatusError(CodeForbidden, "permission denied")
	}
	return principal, nil
}

func mapListInput(request *knowledgerpc.DocumentListRequest) knowledgeapp.ListInput {
	return knowledgeapp.ListInput{
		Query:    request.GetQuery(),
		Page:     int(request.GetPage()),
		PageSize: int(request.GetPageSize()),
	}
}

func mapList(list domain.List) *knowledgerpc.DocumentList {
	items := make([]*knowledgerpc.Document, 0, len(list.Items))
	for _, document := range list.Items {
		items = append(items, mapDocument(document))
	}
	return &knowledgerpc.DocumentList{
		Items:    items,
		Total:    list.Total,
		Page:     int32(list.Page),
		PageSize: int32(list.PageSize),
	}
}

func mapDetail(detail *domain.Detail) *knowledgerpc.DocumentDetail {
	if detail == nil {
		return nil
	}
	blocks := make([]*knowledgerpc.DocumentBlock, 0, len(detail.Blocks))
	for _, block := range detail.Blocks {
		blocks = append(blocks, mapBlock(block))
	}
	return &knowledgerpc.DocumentDetail{Document: mapDocument(detail.Document), Blocks: blocks}
}

func mapDocument(document *domain.Document) *knowledgerpc.Document {
	if document == nil {
		return nil
	}
	result := &knowledgerpc.Document{
		Id:             document.ID,
		Title:          document.Title,
		Summary:        document.Summary,
		Slug:           document.Slug,
		Status:         document.Status,
		AuthorId:       document.AuthorID,
		CurrentVersion: document.CurrentVersion,
		CreatedAtUnix:  document.CreatedAt.UTC().Unix(),
		UpdatedAtUnix:  document.UpdatedAt.UTC().Unix(),
	}
	if document.PublishedAt != nil {
		publishedAt := document.PublishedAt.UTC().Unix()
		result.PublishedAtUnix = &publishedAt
	}
	return result
}

func mapBlock(block *domain.Block) *knowledgerpc.DocumentBlock {
	if block == nil {
		return nil
	}
	return &knowledgerpc.DocumentBlock{
		BlockId:       block.BlockID,
		DocumentId:    block.DocumentID,
		PositionKey:   block.PositionKey,
		Type:          block.Type,
		ContentJson:   block.ContentJSON,
		TextContent:   block.TextContent,
		Version:       block.Version,
		UpdatedBy:     block.UpdatedBy,
		UpdatedAtUnix: block.UpdatedAt.UTC().Unix(),
	}
}

func invalidRequest(message string) error {
	return kerrors.NewBizStatusError(CodeInvalidInput, message)
}

func mapError(ctx context.Context, err error) error {
	var validationError *domain.ValidationError
	switch {
	case errors.As(err, &validationError):
		return kerrors.NewBizStatusError(CodeInvalidInput, validationError.Error())
	case errors.Is(err, knowledgeapp.ErrDocumentNotFound):
		return kerrors.NewBizStatusError(CodeNotFound, "document not found")
	case errors.Is(err, knowledgeapp.ErrVersionConflict):
		return kerrors.NewBizStatusError(CodeConflict, "document version conflict")
	default:
		return internalError(ctx, err)
	}
}

func internalError(ctx context.Context, err error) error {
	klog.CtxErrorf(ctx, "knowledge request failed: %v", err)
	return kerrors.NewBizStatusError(CodeInternal, "internal service error")
}

package rpc

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	attachmenterrors "github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/service"
)

type Readiness interface{ Ready(context.Context) error }
type Handler struct {
	service   *service.Service
	verifier  *coreauth.Verifier
	readiness Readiness
	logger    *slog.Logger
	now       func() time.Time
}

func NewHandler(svc *service.Service, verifier *coreauth.Verifier, readiness Readiness, logger *slog.Logger) (*Handler, error) {
	if svc == nil || verifier == nil || readiness == nil || logger == nil {
		return nil, errors.New("attachment RPC handler dependencies are required")
	}
	return &Handler{service: svc, verifier: verifier, readiness: readiness, logger: logger, now: time.Now}, nil
}
func (h *Handler) Ping(ctx context.Context, req *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	status := "ready"
	if err := h.readiness.Ready(ctx); err != nil {
		status = "not_ready"
	}
	return &commonv1.PingResponse{Service: "attachment", Status: status, UnixTime: h.now().UTC().Unix()}, nil
}
func (h *Handler) Live(ctx context.Context, req *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	return &commonv1.PingResponse{Service: "attachment", Status: "live", UnixTime: h.now().UTC().Unix()}, nil
}
func (h *Handler) CreateAttachment(ctx context.Context, req *attachmentv1.CreateAttachmentRequest) (*attachmentv1.AttachmentUpload, error) {
	owner, err := h.owner(ctx, req != nil)
	if err != nil {
		return nil, err
	}
	out, err := h.service.Create(ctx, owner, req)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return out, nil
}
func (h *Handler) CompleteAttachment(ctx context.Context, req *attachmentv1.CompleteAttachmentRequest) (*attachmentv1.Attachment, error) {
	owner, err := h.owner(ctx, req != nil)
	if err != nil {
		return nil, err
	}
	out, err := h.service.Complete(ctx, owner, req)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return out, nil
}
func (h *Handler) ListAttachments(ctx context.Context, req *attachmentv1.ListAttachmentsRequest) (*attachmentv1.AttachmentList, error) {
	owner, err := h.owner(ctx, req != nil)
	if err != nil {
		return nil, err
	}
	out, err := h.service.List(ctx, owner, req)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return out, nil
}
func (h *Handler) GetAttachment(ctx context.Context, req *attachmentv1.AttachmentIDRequest) (*attachmentv1.Attachment, error) {
	owner, err := h.owner(ctx, req != nil)
	if err != nil {
		return nil, err
	}
	var id string
	if req != nil {
		id = req.AttachmentId
	}
	out, err := h.service.Get(ctx, owner, id)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return out, nil
}
func (h *Handler) TrashAttachment(ctx context.Context, req *attachmentv1.AttachmentIDRequest) error {
	owner, err := h.owner(ctx, req != nil)
	if err != nil {
		return err
	}
	var id string
	if req != nil {
		id = req.AttachmentId
	}
	if err := h.service.Trash(ctx, owner, id); err != nil {
		return h.mapError(ctx, err)
	}
	return nil
}
func (h *Handler) RestoreAttachment(ctx context.Context, req *attachmentv1.AttachmentIDRequest) (*attachmentv1.Attachment, error) {
	owner, err := h.owner(ctx, req != nil)
	if err != nil {
		return nil, err
	}
	var id string
	if req != nil {
		id = req.AttachmentId
	}
	if err := h.service.Restore(ctx, owner, id); err != nil {
		return nil, h.mapError(ctx, err)
	}
	out, err := h.service.Get(ctx, owner, id)
	if err != nil {
		return nil, h.mapError(ctx, err)
	}
	return out, nil
}
func (h *Handler) owner(ctx context.Context, valid bool) (int64, error) {
	if !valid {
		return 0, apperror.ToKitexBizStatus(ctx, attachmenterrors.InvalidInput.New())
	}
	token := strings.TrimSpace(coreauth.AccessToken(ctx))
	if token == "" {
		return 0, apperror.ToKitexBizStatus(ctx, attachmenterrors.Unauthenticated.New())
	}
	principal, err := h.verifier.Verify(token)
	if err != nil || principal.UserID <= 0 {
		return 0, apperror.ToKitexBizStatus(ctx, attachmenterrors.Unauthenticated.Wrap(err))
	}
	return principal.UserID, nil
}
func (h *Handler) mapError(ctx context.Context, err error) error {
	if _, ok := apperror.Details(err); !ok {
		err = attachmenterrors.Internal.Wrap(err)
	}
	return apperror.ToKitexBizStatus(ctx, err)
}

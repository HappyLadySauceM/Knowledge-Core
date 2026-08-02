package internalhttp

import (
	"context"
	"errors"
	"mime"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	knowledgelogic "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/logic"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type CollaborationService interface {
	Authorize(context.Context, string, int64) (*knowledgelogic.CollaborationAuthorization, error)
	Project(context.Context, string, int64, domain.RichTextDocument, string) error
}

type TokenVerifier interface {
	Verify(string) (coreauth.Principal, error)
}

type Handler struct {
	collaboration CollaborationService
	verifier      TokenVerifier
}

type authorizationResponse struct {
	DocumentID         string      `json:"document_id"`
	Access             string      `json:"access"`
	User               *publicUser `json:"user,omitempty"`
	TokenExpiresAt     *string     `json:"token_expires_at,omitempty"`
	PermissionRevision int64       `json:"permission_revision"`
}

type publicUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

type projectionRequest struct {
	Sequence  int64                   `json:"sequence"`
	Content   domain.RichTextDocument `json:"content"`
	PlainText string                  `json:"plain_text"`
}

func NewHandler(collaboration CollaborationService, verifier TokenVerifier) (*Handler, error) {
	if collaboration == nil || verifier == nil {
		return nil, errors.New("create knowledge internal handler: collaboration service and verifier are required")
	}
	return &Handler{collaboration: collaboration, verifier: verifier}, nil
}

func (h *Handler) Authorize(ctx context.Context, request *app.RequestContext) {
	ctx = metadata.EnsureRequestID(ctx)
	documentID := request.Param("document_id")
	actorID, principal, token, err := h.optionalPrincipal(request)
	if err != nil {
		apperror.WriteHertzError(ctx, request, knowledgeerrors.Unauthenticated.Wrap(err))
		return
	}
	if token != "" {
		ctx = coreauth.WithAccessToken(ctx, token)
	}
	authorization, err := h.collaboration.Authorize(ctx, documentID, actorID)
	if err != nil {
		apperror.WriteHertzError(ctx, request, err)
		return
	}
	if authorization == nil || authorization.Document == nil {
		apperror.WriteHertzError(ctx, request, knowledgeerrors.Internal.New())
		return
	}
	response := &authorizationResponse{
		DocumentID: authorization.Document.ID, Access: authorization.Document.Access,
		PermissionRevision: authorization.Document.PermissionRevision,
	}
	if authorization.User != nil {
		response.User = &publicUser{ID: authorization.User.ID, Username: authorization.User.Username, Avatar: authorization.User.Avatar}
	}
	if actorID > 0 && !principal.ExpiresAt.IsZero() {
		expiresAt := principal.ExpiresAt.UTC().Format(time.RFC3339Nano)
		response.TokenExpiresAt = &expiresAt
	}
	writeJSON(request, consts.StatusOK, response)
}

func (h *Handler) Project(ctx context.Context, request *app.RequestContext) {
	ctx = metadata.EnsureRequestID(ctx)
	mediaType, _, err := mime.ParseMediaType(string(request.GetHeader("Content-Type")))
	if err != nil || mediaType != consts.MIMEApplicationJSON {
		apperror.WriteHertzError(ctx, request, knowledgeerrors.InvalidInput.New())
		return
	}
	var input projectionRequest
	if len(request.Request.Body()) == 0 || jsoncodec.Unmarshal(request.Request.Body(), &input) != nil {
		apperror.WriteHertzError(ctx, request, knowledgeerrors.InvalidInput.New())
		return
	}
	if err := h.collaboration.Project(ctx, request.Param("document_id"), input.Sequence, input.Content, input.PlainText); err != nil {
		apperror.WriteHertzError(ctx, request, err)
		return
	}
	request.Status(consts.StatusNoContent)
}

func (h *Handler) optionalPrincipal(request *app.RequestContext) (int64, coreauth.Principal, string, error) {
	header := strings.TrimSpace(string(request.GetHeader("Authorization")))
	if header == "" {
		return 0, coreauth.Principal{}, "", nil
	}
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > coreauth.MaxTokenLength {
		return 0, coreauth.Principal{}, "", errors.New("invalid bearer authorization")
	}
	principal, err := h.verifier.Verify(parts[1])
	if err != nil || principal.UserID <= 0 {
		if err == nil {
			err = errors.New("invalid bearer principal")
		}
		return 0, coreauth.Principal{}, "", err
	}
	return principal.UserID, principal, parts[1], nil
}

func writeJSON(request *app.RequestContext, status int, value any) {
	payload, err := jsoncodec.Marshal(value)
	if err != nil {
		request.Data(consts.StatusInternalServerError, apperror.ProblemContentType,
			[]byte(`{"type":"urn:knowledge-core:problem:common.internal","title":"Internal Server Error","status":500,"detail":"internal server error","code":1,"key":"common.internal"}`))
		return
	}
	request.Data(status, consts.MIMEApplicationJSONUTF8, payload)
}

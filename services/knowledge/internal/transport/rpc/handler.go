package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	commonv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/common"
	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	apperror "github.com/HappyLadySauce/Knowledge-Core/pkg/error"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	knowledgeerrors "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/errors"
	knowledgelogic "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/logic"
)

const serviceName = "knowledge"

type DocumentService interface {
	ListPublished(context.Context, knowledgelogic.ListDocumentsInput) (knowledgelogic.DocumentPage, error)
	GetPublished(context.Context, string, int64) (*knowledgelogic.DocumentDetail, error)
	List(context.Context, knowledgelogic.ListDocumentsInput) (knowledgelogic.DocumentPage, error)
	ListDeleted(context.Context, knowledgelogic.ListDocumentsInput) (knowledgelogic.DocumentPage, error)
	Create(context.Context, knowledgelogic.CreateDocumentInput) (*domain.Document, error)
	Get(context.Context, string, int64) (*domain.Document, error)
	Update(context.Context, knowledgelogic.UpdateDocumentInput) (*domain.Document, error)
	SetPublication(context.Context, string, int64, int64, bool) (*domain.Document, error)
	Delete(context.Context, string, int64, int64) (*domain.Document, error)
	Restore(context.Context, string, int64) (*domain.Document, error)
}

type MemberService interface {
	List(context.Context, string, int64) ([]*domain.Member, error)
	Add(context.Context, knowledgelogic.AddMemberInput) (*domain.Member, error)
	Update(context.Context, string, int64, int64, int64, string) (*domain.Member, error)
	Delete(context.Context, string, int64, int64, int64) error
}

type AttachmentService interface {
	List(context.Context, string, int64) ([]*domain.Attachment, error)
	Create(context.Context, knowledgelogic.CreateAttachmentInput) (*domain.AttachmentUpload, error)
	Complete(context.Context, string, string, int64) (*domain.Attachment, error)
	Delete(context.Context, string, string, int64) error
	Content(context.Context, string, int64) (*domain.AttachmentContent, error)
}

type CollaborationService interface {
	Authorize(context.Context, string, int64) (*knowledgelogic.CollaborationAuthorization, error)
	Project(context.Context, string, int64, domain.RichTextDocument, string) error
}

type TokenVerifier interface {
	Verify(string) (coreauth.Principal, error)
}

type Readiness interface {
	Ready(context.Context) error
}

type Handler struct {
	documents     DocumentService
	members       MemberService
	attachments   AttachmentService
	collaboration CollaborationService
	verifier      TokenVerifier
	readiness     Readiness
	logger        *slog.Logger
	now           func() time.Time
}

func NewHandler(
	documents DocumentService,
	members MemberService,
	attachments AttachmentService,
	collaboration CollaborationService,
	verifier TokenVerifier,
	readiness Readiness,
	logger *slog.Logger,
) (*Handler, error) {
	if documents == nil || members == nil || attachments == nil || collaboration == nil || verifier == nil || readiness == nil || logger == nil {
		return nil, errors.New("create knowledge RPC handler: use cases, verifier, readiness, and logger are required")
	}
	return &Handler{
		documents: documents, members: members, attachments: attachments, collaboration: collaboration,
		verifier: verifier, readiness: readiness, logger: logger, now: time.Now,
	}, nil
}

func (h *Handler) Ping(ctx context.Context, request *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, h.invalidInput(ctx)
	}
	status := "ready"
	if err := h.readiness.Ready(ctx); err != nil {
		status = "not_ready"
	}
	return &commonv1.PingResponse{Service: serviceName, Status: status, UnixTime: h.now().UTC().Unix()}, nil
}

func (h *Handler) Live(ctx context.Context, request *commonv1.PingRequest) (*commonv1.PingResponse, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, h.invalidInput(ctx)
	}
	return &commonv1.PingResponse{Service: serviceName, Status: "live", UnixTime: h.now().UTC().Unix()}, nil
}

func (h *Handler) ListPublishedDocuments(ctx context.Context, request *knowledgev1.ListDocumentsRequest) (*knowledgev1.DocumentPage, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, h.invalidInput(ctx)
	}
	actorID, err := h.optionalActor(ctx)
	if err != nil {
		return nil, err
	}
	page, serviceErr := h.documents.ListPublished(ctx, listInput(request, actorID))
	if serviceErr != nil {
		return nil, h.transportError(ctx, "list_published_documents_failed", serviceErr)
	}
	return toTransportDocumentPage(page), nil
}

func (h *Handler) GetPublishedDocument(ctx context.Context, request *knowledgev1.GetPublishedDocumentRequest) (*knowledgev1.DocumentDetail, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, h.invalidInput(ctx)
	}
	actorID, err := h.optionalActor(ctx)
	if err != nil {
		return nil, err
	}
	detail, serviceErr := h.documents.GetPublished(ctx, request.Slug, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "get_published_document_failed", serviceErr)
	}
	if detail == nil || detail.Document == nil {
		return nil, h.transportError(ctx, "get_published_document_failed", errors.New("knowledge returned an incomplete document detail"))
	}
	return toTransportDocumentDetail(detail), nil
}

func (h *Handler) ListDocuments(ctx context.Context, request *knowledgev1.ListDocumentsRequest) (*knowledgev1.DocumentPage, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	page, serviceErr := h.documents.List(ctx, listInput(request, actorID))
	if serviceErr != nil {
		return nil, h.transportError(ctx, "list_documents_failed", serviceErr)
	}
	return toTransportDocumentPage(page), nil
}

func (h *Handler) CreateDocument(ctx context.Context, request *knowledgev1.CreateDocumentRequest) (*knowledgev1.Document, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if _, err := h.requireActor(ctx, request != nil); err != nil {
		return nil, err
	}
	document, serviceErr := h.documents.Create(ctx, knowledgelogic.CreateDocumentInput{
		Title: request.Title, Summary: request.Summary, Slug: request.Slug,
		IdempotencyKey: stringValue(request.IdempotencyKey),
	})
	if serviceErr != nil {
		return nil, h.transportError(ctx, "create_document_failed", serviceErr)
	}
	return h.documentResult(ctx, "create_document_failed", document)
}

func (h *Handler) GetDocument(ctx context.Context, request *knowledgev1.DocumentIDRequest) (*knowledgev1.Document, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	document, serviceErr := h.documents.Get(ctx, request.DocumentId, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "get_document_failed", serviceErr)
	}
	return h.documentResult(ctx, "get_document_failed", document)
}

func (h *Handler) UpdateDocument(ctx context.Context, request *knowledgev1.UpdateDocumentRequest) (*knowledgev1.Document, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	document, serviceErr := h.documents.Update(ctx, knowledgelogic.UpdateDocumentInput{
		DocumentID: request.DocumentId, ActorID: actorID, ExpectedRevision: request.ExpectedRevision,
		Title: request.Title, Summary: request.Summary, Slug: request.Slug,
	})
	if serviceErr != nil {
		return nil, h.transportError(ctx, "update_document_failed", serviceErr)
	}
	return h.documentResult(ctx, "update_document_failed", document)
}

func (h *Handler) SetPublication(ctx context.Context, request *knowledgev1.SetPublicationRequest) (*knowledgev1.Document, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	document, serviceErr := h.documents.SetPublication(ctx, request.DocumentId, actorID, request.ExpectedRevision, request.Published)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "set_publication_failed", serviceErr)
	}
	return h.documentResult(ctx, "set_publication_failed", document)
}

func (h *Handler) DeleteDocument(ctx context.Context, request *knowledgev1.DeleteDocumentRequest) (*knowledgev1.Document, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	document, serviceErr := h.documents.Delete(ctx, request.DocumentId, actorID, request.ExpectedRevision)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "delete_document_failed", serviceErr)
	}
	return h.documentResult(ctx, "delete_document_failed", document)
}

func (h *Handler) RestoreDeletedDocument(ctx context.Context, request *knowledgev1.DocumentIDRequest) (*knowledgev1.Document, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	document, serviceErr := h.documents.Restore(ctx, request.DocumentId, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "restore_document_failed", serviceErr)
	}
	return h.documentResult(ctx, "restore_document_failed", document)
}

func (h *Handler) ListDeletedDocuments(ctx context.Context, request *knowledgev1.ListDocumentsRequest) (*knowledgev1.DocumentPage, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	page, serviceErr := h.documents.ListDeleted(ctx, listInput(request, actorID))
	if serviceErr != nil {
		return nil, h.transportError(ctx, "list_deleted_documents_failed", serviceErr)
	}
	return toTransportDocumentPage(page), nil
}

func (h *Handler) ListMembers(ctx context.Context, request *knowledgev1.DocumentIDRequest) (*knowledgev1.MemberList, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	members, serviceErr := h.members.List(ctx, request.DocumentId, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "list_members_failed", serviceErr)
	}
	result := make([]*knowledgev1.Member, 0, len(members))
	for _, member := range members {
		result = append(result, toTransportMember(member))
	}
	return &knowledgev1.MemberList{Items: result}, nil
}

func (h *Handler) AddMember(ctx context.Context, request *knowledgev1.AddMemberRequest) (*knowledgev1.Member, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	member, serviceErr := h.members.Add(ctx, knowledgelogic.AddMemberInput{
		DocumentID: request.DocumentId, ActorID: actorID, Username: request.Username,
		Role: request.Role, IdempotencyKey: stringValue(request.IdempotencyKey),
	})
	if serviceErr != nil {
		return nil, h.transportError(ctx, "add_member_failed", serviceErr)
	}
	if member == nil {
		return nil, h.transportError(ctx, "add_member_failed", errors.New("knowledge returned a nil member"))
	}
	return toTransportMember(member), nil
}

func (h *Handler) UpdateMember(ctx context.Context, request *knowledgev1.UpdateMemberRequest) (*knowledgev1.Member, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	member, serviceErr := h.members.Update(
		ctx, request.DocumentId, actorID, request.UserId, request.ExpectedRevision, request.Role,
	)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "update_member_failed", serviceErr)
	}
	if member == nil {
		return nil, h.transportError(ctx, "update_member_failed", errors.New("knowledge returned a nil member"))
	}
	return toTransportMember(member), nil
}

func (h *Handler) DeleteMember(ctx context.Context, request *knowledgev1.DeleteMemberRequest) error {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return err
	}
	if serviceErr := h.members.Delete(ctx, request.DocumentId, actorID, request.UserId, request.ExpectedRevision); serviceErr != nil {
		return h.transportError(ctx, "delete_member_failed", serviceErr)
	}
	return nil
}

func (h *Handler) ListAttachments(ctx context.Context, request *knowledgev1.DocumentIDRequest) (*knowledgev1.AttachmentList, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	attachments, serviceErr := h.attachments.List(ctx, request.DocumentId, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "list_attachments_failed", serviceErr)
	}
	result := make([]*knowledgev1.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		result = append(result, toTransportAttachment(attachment))
	}
	return &knowledgev1.AttachmentList{Items: result}, nil
}

func (h *Handler) CreateAttachment(ctx context.Context, request *knowledgev1.CreateAttachmentRequest) (*knowledgev1.AttachmentUpload, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	upload, serviceErr := h.attachments.Create(ctx, knowledgelogic.CreateAttachmentInput{
		DocumentID: request.DocumentId, ActorID: actorID, Filename: request.Filename,
		MediaType: request.MediaType, SizeBytes: request.SizeBytes, SHA256: request.Sha256,
		IdempotencyKey: stringValue(request.IdempotencyKey),
	})
	if serviceErr != nil {
		return nil, h.transportError(ctx, "create_attachment_failed", serviceErr)
	}
	if upload == nil || upload.Attachment == nil {
		return nil, h.transportError(ctx, "create_attachment_failed", errors.New("knowledge returned an incomplete attachment upload"))
	}
	return &knowledgev1.AttachmentUpload{
		Attachment: toTransportAttachment(upload.Attachment), UploadUrl: upload.URL,
		RequiredHeaders: upload.RequiredHeaders, ExpiresAt: upload.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (h *Handler) CompleteAttachment(ctx context.Context, request *knowledgev1.AttachmentIDRequest) (*knowledgev1.Attachment, error) {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	attachment, serviceErr := h.attachments.Complete(ctx, request.DocumentId, request.AttachmentId, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "complete_attachment_failed", serviceErr)
	}
	if attachment == nil {
		return nil, h.transportError(ctx, "complete_attachment_failed", errors.New("knowledge returned a nil attachment"))
	}
	return toTransportAttachment(attachment), nil
}

func (h *Handler) DeleteAttachment(ctx context.Context, request *knowledgev1.AttachmentIDRequest) error {
	ctx = metadata.EnsureRequestID(ctx)
	actorID, err := h.requireActor(ctx, request != nil)
	if err != nil {
		return err
	}
	if serviceErr := h.attachments.Delete(ctx, request.DocumentId, request.AttachmentId, actorID); serviceErr != nil {
		return h.transportError(ctx, "delete_attachment_failed", serviceErr)
	}
	return nil
}

func (h *Handler) GetAttachmentContent(ctx context.Context, request *knowledgev1.AttachmentContentRequest) (*knowledgev1.AttachmentContent, error) {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil {
		return nil, h.invalidInput(ctx)
	}
	actorID, err := h.optionalActor(ctx)
	if err != nil {
		return nil, err
	}
	content, serviceErr := h.attachments.Content(ctx, request.AttachmentId, actorID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "get_attachment_content_failed", serviceErr)
	}
	if content == nil || content.URL == "" || content.ExpiresAt.IsZero() {
		return nil, h.transportError(ctx, "get_attachment_content_failed", errors.New("knowledge returned incomplete attachment content"))
	}
	return &knowledgev1.AttachmentContent{Url: content.URL, ExpiresAt: content.ExpiresAt.UTC().Format(time.RFC3339Nano)}, nil
}

func (h *Handler) AuthorizeCollaboration(
	ctx context.Context,
	request *knowledgev1.AuthorizeCollaborationRequest,
) (*knowledgev1.CollaborationAuthorization, error) {
	ctx = metadata.EnsureRequestID(ctx)
	principal, err := h.requirePrincipal(ctx, request != nil)
	if err != nil {
		return nil, err
	}
	authorization, serviceErr := h.collaboration.Authorize(ctx, request.DocumentId, principal.UserID)
	if serviceErr != nil {
		return nil, h.transportError(ctx, "authorize_collaboration_failed", serviceErr)
	}
	if authorization == nil || authorization.Document == nil || authorization.User == nil ||
		authorization.Document.ID != request.DocumentId || authorization.User.ID != principal.UserID ||
		(authorization.Document.Access != domain.AccessOwner && authorization.Document.Access != domain.AccessEditor &&
			authorization.Document.Access != domain.AccessViewer) || principal.ExpiresAt.IsZero() {
		return nil, h.transportError(ctx, "authorize_collaboration_failed", errors.New("knowledge returned incomplete collaboration authorization"))
	}
	return &knowledgev1.CollaborationAuthorization{
		DocumentId:         authorization.Document.ID,
		Actor:              toTransportUser(*authorization.User),
		Access:             authorization.Document.Access,
		PermissionRevision: authorization.Document.PermissionRevision,
		TokenExpiresAt:     principal.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (h *Handler) ProjectCollaboration(ctx context.Context, request *knowledgev1.ProjectCollaborationRequest) error {
	ctx = metadata.EnsureRequestID(ctx)
	if request == nil || request.Content == nil {
		return h.invalidInput(ctx)
	}
	content, err := fromTransportRichText(request.Content)
	if err != nil {
		return h.invalidInput(ctx)
	}
	if err := h.collaboration.Project(
		ctx, request.DocumentId, request.Sequence, content, request.PlainText,
	); err != nil {
		return h.transportError(ctx, "project_collaboration_failed", err)
	}
	return nil
}

func (h *Handler) documentResult(ctx context.Context, event string, document *domain.Document) (*knowledgev1.Document, error) {
	if document == nil {
		return nil, h.transportError(ctx, event, errors.New("knowledge returned a nil document"))
	}
	return toTransportDocument(document), nil
}

func (h *Handler) optionalActor(ctx context.Context) (int64, error) {
	token := strings.TrimSpace(coreauth.AccessToken(ctx))
	if token == "" {
		return 0, nil
	}
	principal, err := h.verifier.Verify(token)
	if err != nil || principal.UserID <= 0 {
		return 0, apperror.ToKitexBizStatus(ctx, knowledgeerrors.Unauthenticated.Wrap(err))
	}
	return principal.UserID, nil
}

func (h *Handler) requireActor(ctx context.Context, validRequest bool) (int64, error) {
	principal, err := h.requirePrincipal(ctx, validRequest)
	if err != nil {
		return 0, err
	}
	return principal.UserID, nil
}

func (h *Handler) requirePrincipal(ctx context.Context, validRequest bool) (coreauth.Principal, error) {
	if !validRequest {
		return coreauth.Principal{}, h.invalidInput(ctx)
	}
	token := strings.TrimSpace(coreauth.AccessToken(ctx))
	if token == "" {
		return coreauth.Principal{}, apperror.ToKitexBizStatus(ctx, knowledgeerrors.Unauthenticated.New())
	}
	principal, err := h.verifier.Verify(token)
	if err != nil || principal.UserID <= 0 || principal.ExpiresAt.IsZero() {
		return coreauth.Principal{}, apperror.ToKitexBizStatus(ctx, knowledgeerrors.Unauthenticated.Wrap(err))
	}
	return principal, nil
}

func (h *Handler) invalidInput(ctx context.Context) error {
	return apperror.ToKitexBizStatus(ctx, knowledgeerrors.InvalidInput.New())
}

func (h *Handler) transportError(ctx context.Context, event string, err error) error {
	mapped := err
	if _, ok := apperror.Details(mapped); !ok {
		mapped = knowledgeerrors.Internal.Wrap(err)
	}
	level := slog.LevelWarn
	if apperror.KindOf(mapped) == apperror.KindInternal {
		level = slog.LevelError
	}
	h.logger.Log(ctx, level, "knowledge RPC operation failed",
		slog.String("component", "knowledge.rpc"),
		slog.String("event", event),
		slog.String("error_key", apperror.Key(mapped)),
		slog.String("error.type", fmt.Sprintf("%T", err)),
	)
	return apperror.ToKitexBizStatus(ctx, mapped)
}

func listInput(request *knowledgev1.ListDocumentsRequest, actorID int64) knowledgelogic.ListDocumentsInput {
	return knowledgelogic.ListDocumentsInput{
		ActorID: actorID, Query: stringValue(request.Query), Cursor: stringValue(request.Cursor),
		Limit: int32Value(request.Limit), Access: stringValue(request.Access), Publication: stringValue(request.Publication),
	}
}

func toTransportDocumentPage(page knowledgelogic.DocumentPage) *knowledgev1.DocumentPage {
	items := make([]*knowledgev1.Document, 0, len(page.Items))
	for _, document := range page.Items {
		items = append(items, toTransportDocument(document))
	}
	return &knowledgev1.DocumentPage{
		Items: items,
		Page:  &knowledgev1.PageInfo{NextCursor: page.NextCursor, HasMore: page.HasMore},
	}
}

func toTransportDocument(value *domain.Document) *knowledgev1.Document {
	if value == nil {
		return nil
	}
	return &knowledgev1.Document{
		Id: value.ID, Title: value.Title, Summary: value.Summary, Slug: value.Slug,
		Owner: toTransportUser(value.Owner), Access: value.Access, Published: value.Published,
		MetadataRevision: value.MetadataRevision, ContentRevision: value.ContentRevision,
		PublishedAt: timePointer(value.PublishedAt), DeletedAt: timePointer(value.DeletedAt),
		ProjectedAt: timePointer(value.ProjectedAt), CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toTransportDocumentDetail(value *knowledgelogic.DocumentDetail) *knowledgev1.DocumentDetail {
	attachments := make([]*knowledgev1.Attachment, 0, len(value.Attachments))
	for _, attachment := range value.Attachments {
		attachments = append(attachments, toTransportAttachment(attachment))
	}
	return &knowledgev1.DocumentDetail{
		Document: toTransportDocument(value.Document), Content: toTransportRichText(value.Content),
		PlainText: value.PlainText, Attachments: attachments,
	}
}

func toTransportUser(value domain.PublicUser) *knowledgev1.PublicUser {
	return &knowledgev1.PublicUser{Id: value.ID, Username: value.Username, Avatar: value.Avatar}
}

func toTransportMember(value *domain.Member) *knowledgev1.Member {
	if value == nil {
		return nil
	}
	return &knowledgev1.Member{
		User: toTransportUser(value.User), Role: value.Role, Revision: value.Revision,
		CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toTransportAttachment(value *domain.Attachment) *knowledgev1.Attachment {
	if value == nil {
		return nil
	}
	mediaType := value.DetectedType
	if mediaType == "" {
		mediaType = value.DeclaredType
	}
	return &knowledgev1.Attachment{
		Id: value.ID, DocumentId: value.DocumentID, Filename: value.Filename,
		MediaType: mediaType, SizeBytes: value.SizeBytes, Status: value.Status,
		CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func toTransportRichText(value domain.RichTextDocument) *knowledgev1.RichTextDocument {
	content := make([]*knowledgev1.RichTextNode, 0, len(value.Content))
	for _, node := range value.Content {
		content = append(content, toTransportRichTextNode(node))
	}
	return &knowledgev1.RichTextDocument{Type: value.Type, Content: content}
}

func toTransportRichTextNode(value *domain.RichTextNode) *knowledgev1.RichTextNode {
	if value == nil {
		return nil
	}
	content := make([]*knowledgev1.RichTextNode, 0, len(value.Content))
	for _, child := range value.Content {
		content = append(content, toTransportRichTextNode(child))
	}
	marks := make([]*knowledgev1.RichTextMark, 0, len(value.Marks))
	for index := range value.Marks {
		marks = append(marks, &knowledgev1.RichTextMark{Type: value.Marks[index].Type, Attrs: toTransportRichTextAttrs(value.Marks[index].Attrs)})
	}
	return &knowledgev1.RichTextNode{
		Type: value.Type, Attrs: toTransportRichTextAttrs(value.Attrs), Content: content, Text: value.Text, Marks: marks,
	}
}

func toTransportRichTextAttrs(value *domain.RichTextAttrs) *knowledgev1.RichTextAttrs {
	if value == nil {
		return nil
	}
	return &knowledgev1.RichTextAttrs{
		Level: value.Level, Start: value.Start, Checked: value.Checked, Language: value.Language,
		Href: value.Href, AttachmentId: value.AttachmentID, Alt: value.Alt, Title: value.Title,
		TextAlign: value.TextAlign, Colspan: value.Colspan, Rowspan: value.Rowspan,
		Colwidth: append([]int32(nil), value.Colwidth...),
	}
}

func fromTransportRichText(value *knowledgev1.RichTextDocument) (domain.RichTextDocument, error) {
	if value == nil {
		return domain.RichTextDocument{}, errors.New("rich-text document is required")
	}
	count := 0
	content := make([]*domain.RichTextNode, 0, len(value.Content))
	for _, node := range value.Content {
		converted, err := fromTransportRichTextNode(node, 1, &count)
		if err != nil {
			return domain.RichTextDocument{}, err
		}
		content = append(content, converted)
	}
	return domain.RichTextDocument{Type: value.Type, Content: content}, nil
}

func fromTransportRichTextNode(value *knowledgev1.RichTextNode, depth int, count *int) (*domain.RichTextNode, error) {
	if value == nil || depth > 64 || count == nil {
		return nil, errors.New("rich-text node structure is invalid")
	}
	*count++
	if *count > 100000 {
		return nil, errors.New("rich-text document contains too many nodes")
	}
	content := make([]*domain.RichTextNode, 0, len(value.Content))
	for _, child := range value.Content {
		converted, err := fromTransportRichTextNode(child, depth+1, count)
		if err != nil {
			return nil, err
		}
		content = append(content, converted)
	}
	marks := make([]domain.RichTextMark, 0, len(value.Marks))
	for _, mark := range value.Marks {
		if mark == nil {
			marks = append(marks, domain.RichTextMark{})
			continue
		}
		marks = append(marks, domain.RichTextMark{Type: mark.Type, Attrs: fromTransportRichTextAttrs(mark.Attrs)})
	}
	return &domain.RichTextNode{
		Type: value.Type, Attrs: fromTransportRichTextAttrs(value.Attrs), Content: content,
		Text: value.Text, Marks: marks,
	}, nil
}

func fromTransportRichTextAttrs(value *knowledgev1.RichTextAttrs) *domain.RichTextAttrs {
	if value == nil {
		return nil
	}
	return &domain.RichTextAttrs{
		Level: value.Level, Start: value.Start, Checked: value.Checked, Language: value.Language,
		Href: value.Href, AttachmentID: value.AttachmentId, Alt: value.Alt, Title: value.Title,
		TextAlign: value.TextAlign, Colspan: value.Colspan, Rowspan: value.Rowspan,
		Colwidth: append([]int32(nil), value.Colwidth...),
	}
}

func timePointer(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func int32Value(value *int32) int32 {
	if value == nil {
		return 0
	}
	return *value
}

var _ knowledgev1.KnowledgeService = (*Handler)(nil)

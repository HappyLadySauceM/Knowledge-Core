package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func handleListPublishedDocuments(ctx context.Context, request *app.RequestContext) {
	input, err := decodeListInput(request, false)
	if err != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	page, err := dependencies.Knowledge.ListPublishedDocuments(upstreamContext(ctx, request), listRequest(input))
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentPageData(page)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleGetPublishedDocument(ctx context.Context, request *app.RequestContext) {
	slug, err := pathSlug(request)
	if err != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	detail, err := dependencies.Knowledge.GetPublishedDocument(
		upstreamContext(ctx, request), &knowledgev1.GetPublishedDocumentRequest{Slug: slug},
	)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentDetailData(detail, dependencies.Endpoints)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	websocketURL := dependencies.Endpoints.CollaborationWebSocketURL
	data.WebsocketURL = &websocketURL
	request.Header("ETag", formatETag(data.Document.MetadataRevision))
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleGetAttachmentContent(ctx context.Context, request *app.RequestContext) {
	attachmentID, err := pathUUID(request, "attachment_id")
	if err != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	content, err := dependencies.Knowledge.GetAttachmentContent(
		upstreamContext(ctx, request), &knowledgev1.AttachmentContentRequest{AttachmentId: attachmentID},
	)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	if content == nil || !validRFC3339(content.ExpiresAt) || !validRedirectURL(content.Url) {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	gatewaymiddleware.ResponseMetadata(ctx, request)
	request.Header("Cache-Control", "private, no-store")
	request.Header("Location", content.Url)
	request.Status(http.StatusSeeOther)
}

func handleListDocuments(ctx context.Context, request *app.RequestContext) {
	listDocuments(ctx, request, false)
}

func handleListDeletedDocuments(ctx context.Context, request *app.RequestContext) {
	listDocuments(ctx, request, true)
}

func listDocuments(ctx context.Context, request *app.RequestContext, deleted bool) {
	input, err := decodeListInput(request, true)
	if err != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	var page *knowledgev1.DocumentPage
	if deleted {
		page, err = dependencies.Knowledge.ListDeletedDocuments(upstreamContext(ctx, request), listRequest(input))
	} else {
		page, err = dependencies.Knowledge.ListDocuments(upstreamContext(ctx, request), listRequest(input))
	}
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentPageData(page)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCreateDocument(ctx context.Context, request *app.RequestContext) {
	var body createDocumentBody
	idempotency, keyErr := idempotencyKey(request)
	if decodeJSONBody(request, &body) != nil || requireNoQuery(request) != nil || keyErr != nil || strings.TrimSpace(body.Title) == "" {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.CreateDocument(upstreamContext(ctx, request), &knowledgev1.CreateDocumentRequest{
		Title: body.Title, Summary: body.Summary, Slug: body.Slug, IdempotencyKey: optionalString(idempotency),
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentData(document)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Location", endpointURL(dependencies.Endpoints, "/api/v1/studio/documents/"+url.PathEscape(data.ID)))
	writeDocument(ctx, request, consts.StatusCreated, data)
}

func handleGetDocument(ctx context.Context, request *app.RequestContext) {
	documentID, err := pathUUID(request, "document_id")
	if err != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.GetDocument(
		upstreamContext(ctx, request), &knowledgev1.DocumentIDRequest{DocumentId: documentID},
	)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentData(document)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeDocument(ctx, request, consts.StatusOK, data)
}

func handleUpdateDocument(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	revision, revisionErr := expectedRevision(request)
	var body updateDocumentBody
	if pathErr != nil || revisionErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil ||
		(body.Title == nil && body.Summary == nil && body.Slug == nil) {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.UpdateDocument(upstreamContext(ctx, request), &knowledgev1.UpdateDocumentRequest{
		DocumentId: documentID, ExpectedRevision: revision, Title: body.Title, Summary: body.Summary, Slug: body.Slug,
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentData(document)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeDocument(ctx, request, consts.StatusOK, data)
}

func handleDeleteDocument(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	revision, revisionErr := expectedRevision(request)
	if pathErr != nil || revisionErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	if _, err := dependencies.Knowledge.DeleteDocument(upstreamContext(ctx, request), &knowledgev1.DeleteDocumentRequest{
		DocumentId: documentID, ExpectedRevision: revision,
	}); err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	writeNoContent(ctx, request)
}

func handlePublishDocument(ctx context.Context, request *app.RequestContext) {
	setPublication(ctx, request, true)
}

func handleUnpublishDocument(ctx context.Context, request *app.RequestContext) {
	setPublication(ctx, request, false)
}

func setPublication(ctx context.Context, request *app.RequestContext, published bool) {
	documentID, pathErr := pathUUID(request, "document_id")
	revision, revisionErr := expectedRevision(request)
	if pathErr != nil || revisionErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.SetPublication(upstreamContext(ctx, request), &knowledgev1.SetPublicationRequest{
		DocumentId: documentID, ExpectedRevision: revision, Published: published,
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	if !published {
		writeNoContent(ctx, request)
		return
	}
	data, err := toDocumentData(document)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeDocument(ctx, request, consts.StatusOK, data)
}

func handleRestoreDeletedDocument(ctx context.Context, request *app.RequestContext) {
	documentID, err := pathUUID(request, "document_id")
	if err != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.RestoreDeletedDocument(
		upstreamContext(ctx, request), &knowledgev1.DocumentIDRequest{DocumentId: documentID},
	)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentData(document)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeDocument(ctx, request, consts.StatusOK, data)
}

func handleListMembers(ctx context.Context, request *app.RequestContext) {
	documentID, err := pathUUID(request, "document_id")
	if err != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	members, err := dependencies.Knowledge.ListMembers(
		upstreamContext(ctx, request), &knowledgev1.DocumentIDRequest{DocumentId: documentID},
	)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toMemberListData(members)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleAddMember(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	idempotency, keyErr := idempotencyKey(request)
	var body addMemberBody
	if pathErr != nil || keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil ||
		strings.TrimSpace(body.Username) == "" || (body.Role != "viewer" && body.Role != "editor") {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	member, err := dependencies.Knowledge.AddMember(upstreamContext(ctx, request), &knowledgev1.AddMemberRequest{
		DocumentId: documentID, Username: body.Username, Role: body.Role, IdempotencyKey: optionalString(idempotency),
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toMemberData(member)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Location", endpointURL(dependencies.Endpoints, "/api/v1/studio/documents/"+url.PathEscape(documentID)+"/members/"+data.User.ID))
	writeMember(ctx, request, consts.StatusCreated, data)
}

func handleUpdateMember(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	userID, userErr := pathUserID(request)
	revision, revisionErr := expectedRevision(request)
	var body updateMemberBody
	if documentErr != nil || userErr != nil || revisionErr != nil || requireNoQuery(request) != nil ||
		decodeJSONBody(request, &body) != nil || (body.Role != "viewer" && body.Role != "editor") {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	member, err := dependencies.Knowledge.UpdateMember(upstreamContext(ctx, request), &knowledgev1.UpdateMemberRequest{
		DocumentId: documentID, UserId: userID, ExpectedRevision: revision, Role: body.Role,
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toMemberData(member)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeMember(ctx, request, consts.StatusOK, data)
}

func handleDeleteMember(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	userID, userErr := pathUserID(request)
	revision, revisionErr := expectedRevision(request)
	if documentErr != nil || userErr != nil || revisionErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	if err := dependencies.Knowledge.DeleteMember(upstreamContext(ctx, request), &knowledgev1.DeleteMemberRequest{
		DocumentId: documentID, UserId: userID, ExpectedRevision: revision,
	}); err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	writeNoContent(ctx, request)
}

func handleListAttachments(ctx context.Context, request *app.RequestContext) {
	documentID, err := pathUUID(request, "document_id")
	if err != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	attachments, err := dependencies.Knowledge.ListAttachments(
		upstreamContext(ctx, request), &knowledgev1.DocumentIDRequest{DocumentId: documentID},
	)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toAttachmentListData(attachments, dependencies.Endpoints)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCreateAttachment(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	idempotency, keyErr := idempotencyKey(request)
	var body createAttachmentBody
	if pathErr != nil || keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil ||
		body.Filename == "" || body.MediaType == "" || body.SizeBytes <= 0 || body.SHA256 == "" {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	upload, err := dependencies.Knowledge.CreateAttachment(upstreamContext(ctx, request), &knowledgev1.CreateAttachmentRequest{
		DocumentId: documentID, Filename: body.Filename, MediaType: body.MediaType, SizeBytes: body.SizeBytes,
		Sha256: body.SHA256, IdempotencyKey: optionalString(idempotency),
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toAttachmentUploadData(upload, dependencies.Endpoints)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Location", endpointURL(dependencies.Endpoints, "/api/v1/studio/documents/"+url.PathEscape(documentID)+"/attachments/"+url.PathEscape(data.Attachment.ID)))
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func handleCompleteAttachment(ctx context.Context, request *app.RequestContext) {
	mutateAttachment(ctx, request, true)
}

func handleDeleteAttachment(ctx context.Context, request *app.RequestContext) {
	mutateAttachment(ctx, request, false)
}

func mutateAttachment(ctx context.Context, request *app.RequestContext, complete bool) {
	documentID, documentErr := pathUUID(request, "document_id")
	attachmentID, attachmentErr := pathUUID(request, "attachment_id")
	if documentErr != nil || attachmentErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	input := &knowledgev1.AttachmentIDRequest{DocumentId: documentID, AttachmentId: attachmentID}
	if !complete {
		if err := dependencies.Knowledge.DeleteAttachment(upstreamContext(ctx, request), input); err != nil {
			gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
			return
		}
		writeNoContent(ctx, request)
		return
	}
	attachment, err := dependencies.Knowledge.CompleteAttachment(upstreamContext(ctx, request), input)
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toAttachmentData(attachment, dependencies.Endpoints)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func listRequest(input listInput) *knowledgev1.ListDocumentsRequest {
	return &knowledgev1.ListDocumentsRequest{
		Query: input.query, Cursor: input.cursor, Limit: input.limit, Access: input.access, Publication: input.publication,
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func writeJSON(ctx context.Context, request *app.RequestContext, status int, value any) {
	gatewaymiddleware.ResponseMetadata(ctx, request)
	gatewaymiddleware.WriteJSON(request, status, value)
}

func writeDocument(ctx context.Context, request *app.RequestContext, status int, document *gatewaymodel.DocumentData) {
	request.Header("ETag", formatETag(document.MetadataRevision))
	writeJSON(ctx, request, status, document)
}

func writeMember(ctx context.Context, request *app.RequestContext, status int, member *gatewaymodel.MemberData) {
	request.Header("ETag", formatETag(member.Revision))
	writeJSON(ctx, request, status, member)
}

func writeNoContent(ctx context.Context, request *app.RequestContext) {
	gatewaymiddleware.ResponseMetadata(ctx, request)
	request.Status(consts.StatusNoContent)
}

func validRedirectURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed != nil && parsed.IsAbs() && parsed.Host != "" && parsed.User == nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Fragment == ""
}

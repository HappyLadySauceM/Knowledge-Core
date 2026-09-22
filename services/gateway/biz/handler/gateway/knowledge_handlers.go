package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	collaborationv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/collaboration"
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
	data, err := toDocumentDetailData(detail, dependencies.EndpointOptions())
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
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
	if dependencies.Attachment == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	workCtx := upstreamContext(ctx, request)
	authorization, authErr := dependencies.Knowledge.IsMediaPublished(
		workCtx, &knowledgev1.PublishedMediaRequest{AttachmentId: attachmentID},
	)
	if authErr != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, authErr)
		return
	}
	if authorization != nil && authorization.Published {
		content, contentErr := dependencies.Attachment.GetPublishedAttachmentContent(
			workCtx, &attachmentv1.AttachmentIDRequest{AttachmentId: attachmentID},
		)
		if contentErr != nil {
			gatewaymiddleware.WriteAttachmentError(ctx, request, contentErr)
			return
		}
		if content == nil || !validRFC3339(content.ExpiresAt) || !validRedirectURL(content.Url) {
			gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
			return
		}
		gatewaymiddleware.ResponseMetadata(ctx, request)
		request.Header("Cache-Control", "public, max-age=60")
		request.Header("Location", content.Url)
		request.Status(http.StatusSeeOther)
		return
	}
	if _, authenticated := gatewaymiddleware.Principal(request); authenticated {
		attachment, attachmentErr := dependencies.Attachment.GetAttachment(
			workCtx, &attachmentv1.AttachmentIDRequest{AttachmentId: attachmentID},
		)
		if attachmentErr == nil {
			if attachment == nil || attachment.Status != "ready" || attachment.DownloadUrl == nil || attachment.DownloadExpiresAt == nil || !validRFC3339(*attachment.DownloadExpiresAt) || !validRedirectURL(*attachment.DownloadUrl) {
				gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
				return
			}
			gatewaymiddleware.ResponseMetadata(ctx, request)
			request.Header("Cache-Control", "private, no-store")
			request.Header("Location", *attachment.DownloadUrl)
			request.Status(http.StatusSeeOther)
			return
		}
		gatewaymiddleware.WriteAttachmentError(ctx, request, attachmentErr)
		return
	}
	gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrResourceNotFound)
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
	request.Header("Location", endpointURL(dependencies.EndpointOptions(), "/api/v1/studio/documents/"+url.PathEscape(data.ID)))
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
		(body.Title == nil && body.Summary == nil && body.Slug == nil && body.Language == nil && body.Tags == nil && body.FolderID == nil && body.Icon == nil && body.CoverAttachmentID == nil && body.CoverFocalX == nil && body.CoverFocalY == nil) {
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
		Language: body.Language, Tags: append([]string(nil), body.Tags...), FolderId: body.FolderID, Icon: body.Icon,
		CoverAttachmentId: body.CoverAttachmentID, CoverFocalX: body.CoverFocalX, CoverFocalY: body.CoverFocalY,
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
	documentID, pathErr := pathUUID(request, "document_id")
	revision, revisionErr := expectedRevision(request)
	idempotency, keyErr := idempotencyKey(request)
	var body struct {
		StateVector       string   `json:"state_vector"`
		Icon              string   `json:"icon"`
		CoverAttachmentID string   `json:"cover_attachment_id"`
		CoverFocalX       *float64 `json:"cover_focal_x"`
		CoverFocalY       *float64 `json:"cover_focal_y"`
	}
	if pathErr != nil || revisionErr != nil || keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	stateVector, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimSpace(body.StateVector))
	if decodeErr != nil || len(stateVector) == 0 || len(stateVector) > 64*1024 {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	draft, err := dependencies.Knowledge.GetDocument(upstreamContext(ctx, request), &knowledgev1.DocumentIDRequest{DocumentId: documentID})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	captured, err := dependencies.Collaboration.CapturePublicationSnapshot(upstreamContext(ctx, request), &collaborationv1.CapturePublicationSnapshotRequest{
		DocumentId: documentID, StateVector: stateVector,
	})
	if err != nil {
		gatewaymiddleware.WriteCollaborationError(ctx, request, err)
		return
	}
	if captured == nil || captured.Content == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	language := languageOrDefault(draft.Language)
	summary := publicationSummary(draft.Summary, captured.PlainText)
	publicationHash := canonicalPublicationHash(draft.Title, summary, draft.Slug, language, draft.Tags, captured.ContentHash, body.Icon, body.CoverAttachmentID, body.CoverFocalX, body.CoverFocalY)
	published, err := dependencies.Knowledge.PublishSnapshot(upstreamContext(ctx, request), &knowledgev1.PublishSnapshotRequest{
		DocumentId: documentID, ExpectedMetadataRevision: revision,
		Title: draft.Title, Summary: summary, Slug: draft.Slug, Language: language, Tags: append([]string(nil), draft.Tags...),
		Content: captured.Content, PlainText: captured.PlainText, IdempotencyKey: optionalString(idempotency), PublicationHash: &publicationHash,
		Icon: optionalString(body.Icon), CoverAttachmentId: optionalString(body.CoverAttachmentID), CoverFocalX: body.CoverFocalX, CoverFocalY: body.CoverFocalY,
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentData(published)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeDocument(ctx, request, consts.StatusAccepted, data)
}

func languageOrDefault(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "zh-CN"
	}
	return strings.TrimSpace(*value)
}

func publicationSummary(summary, plainText string) string {
	if value := strings.TrimSpace(summary); value != "" {
		return value
	}
	for _, line := range strings.Split(plainText, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			runes := []rune(value)
			if len(runes) > 1000 {
				return string(runes[:1000])
			}
			return value
		}
	}
	return ""
}

func canonicalPublicationHash(title, summary, slug, language string, tags []string, contentHash, icon, cover string, focalX, focalY *float64) string {
	// encoding/json sorts map keys lexicographically.  Keep the Gateway's
	// envelope byte-for-byte compatible with the browser's recursive stable
	// JSON encoder; struct field order is not a portable canonicalization.
	payload, _ := json.Marshal(map[string]any{
		"title":               title,
		"summary":             summary,
		"slug":                slug,
		"language":            language,
		"tags":                tags,
		"content_hash":        contentHash,
		"icon":                icon,
		"cover_attachment_id": cover,
		"cover_focal_x":       focalX,
		"cover_focal_y":       focalY,
	})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func handleUnpublishDocument(ctx context.Context, request *app.RequestContext) {
	setPublication(ctx, request, false)
}

func setPublication(ctx context.Context, request *app.RequestContext, published bool) {
	documentID, pathErr := pathUUID(request, "document_id")
	revision, revisionErr := expectedRevision(request)
	idempotency, keyErr := idempotencyKey(request)
	if pathErr != nil || revisionErr != nil || keyErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.SetPublication(upstreamContext(ctx, request), &knowledgev1.SetPublicationRequest{
		DocumentId: documentID, ExpectedRevision: revision, Published: published, IdempotencyKey: optionalString(idempotency),
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

func handlePermanentlyDeleteDocument(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	revision, revisionErr := expectedRevision(request)
	idempotency, keyErr := idempotencyKey(request)
	confirmationErr := permanentDeleteConfirmation(request)
	if pathErr != nil || revisionErr != nil || keyErr != nil || confirmationErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	if err := dependencies.Knowledge.PurgeDeletedDocument(upstreamContext(ctx, request), &knowledgev1.PurgeDeletedDocumentRequest{
		DocumentId: documentID, ExpectedRevision: revision, IdempotencyKey: optionalString(idempotency),
	}); err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	gatewaymiddleware.ResponseMetadata(ctx, request)
	request.Status(consts.StatusAccepted)
}

func handleListCommits(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	values, queryErr := strictQuery(request, map[string]struct{}{"limit": {}})
	limit, limitErr := parseVersionLimit(queryPointer(values, "limit"))
	if pathErr != nil || queryErr != nil || limitErr != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	page, err := dependencies.Knowledge.ListCommits(upstreamContext(ctx, request), &knowledgev1.ListCommitsRequest{
		DocumentId: documentID, Limit: optionalInt32(limit),
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toCommitPageData(page)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCreateCommit(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	idempotency, keyErr := idempotencyKey(request)
	var body struct {
		Kind        string                             `json:"kind"`
		Label       *string                            `json:"label,omitempty"`
		Description *string                            `json:"description,omitempty"`
		ContentHash *string                            `json:"content_hash,omitempty"`
		Content     *gatewaymodel.RichTextDocumentData `json:"content,omitempty"`
		PlainText   *string                            `json:"plain_text,omitempty"`
	}
	if pathErr != nil || keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil || strings.TrimSpace(body.Kind) == "" {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	kind, kindErr := commitKindFromHTTP(body.Kind)
	if kindErr != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	var content *knowledgev1.RichTextDocument
	if body.Content != nil {
		var contentErr error
		content, contentErr = fromRichTextDocumentData(body.Content)
		if contentErr != nil {
			gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
			return
		}
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	commit, err := dependencies.Knowledge.CreateCommit(upstreamContext(ctx, request), &knowledgev1.CreateCommitRequest{
		DocumentId: documentID, Kind: kind, Label: body.Label, Description: body.Description,
		ContentHash: body.ContentHash, Content: content, PlainText: body.PlainText, IdempotencyKey: optionalString(idempotency),
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toCommitData(commit)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func commitKindFromHTTP(value string) (knowledgev1.CommitKind, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "manual":
		return knowledgev1.CommitKind_MANUAL, nil
	case "leave":
		return knowledgev1.CommitKind_LEAVE, nil
	case "safety":
		return knowledgev1.CommitKind_SAFETY, nil
	case "publish":
		return knowledgev1.CommitKind_PUBLISH, nil
	case "revision_merge":
		return knowledgev1.CommitKind_REVISION_MERGE, nil
	case "agent_edit":
		return knowledgev1.CommitKind_AGENT_EDIT, nil
	case "restore":
		return knowledgev1.CommitKind_RESTORE, nil
	default:
		return 0, errors.New("commit kind is invalid")
	}
}

func handleGetCommit(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	commitID, commitErr := pathUUID(request, "commit_id")
	if documentErr != nil || commitErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	commit, err := dependencies.Knowledge.GetCommit(upstreamContext(ctx, request), &knowledgev1.CommitIDRequest{CommitId: commitID})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toCommitData(commit)
	if err != nil || data.DocumentID != documentID {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrResourceNotFound)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleRenameCommit(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	commitID, commitErr := pathUUID(request, "commit_id")
	var body struct {
		Label       string  `json:"label"`
		Description *string `json:"description,omitempty"`
	}
	if documentErr != nil || commitErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil || strings.TrimSpace(body.Label) == "" {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	commit, err := dependencies.Knowledge.RenameCommit(upstreamContext(ctx, request), &knowledgev1.RenameCommitRequest{
		CommitId: commitID, Label: body.Label, Description: body.Description,
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toCommitData(commit)
	if err != nil || data.DocumentID != documentID {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrResourceNotFound)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleRestoreCommit(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	commitID, commitErr := pathUUID(request, "commit_id")
	idempotency, keyErr := idempotencyKey(request)
	if documentErr != nil || commitErr != nil || keyErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	document, err := dependencies.Knowledge.RestoreCommit(upstreamContext(ctx, request), &knowledgev1.RestoreCommitRequest{
		CommitId: commitID, IdempotencyKey: optionalString(idempotency),
	})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toDocumentData(document)
	if err != nil || data.ID != documentID {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrResourceNotFound)
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
	request.Header("Location", endpointURL(dependencies.EndpointOptions(), "/api/v1/studio/documents/"+url.PathEscape(documentID)+"/members/"+data.User.ID))
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

func listRequest(input listInput) *knowledgev1.ListDocumentsRequest {
	return &knowledgev1.ListDocumentsRequest{
		Query: input.query, Cursor: input.cursor, Limit: input.limit, Access: input.access, Publication: input.publication, FolderId: input.folderID,
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

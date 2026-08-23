package gateway

import (
	"context"
	"net/url"
	"strings"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func handleListMediaAttachments(ctx context.Context, request *app.RequestContext) {
	values, err := strictQuery(request, map[string]struct{}{"status": {}, "category": {}, "cursor": {}, "limit": {}})
	limit, limitErr := parseVersionLimit(queryPointer(values, "limit"))
	status, category, cursor := stringValue(queryPointer(values, "status")), stringValue(queryPointer(values, "category")), stringValue(queryPointer(values, "cursor"))
	if err != nil || limitErr != nil || len(cursor) > 1024 || containsControl(status) || containsControl(category) || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Attachment == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	list, err := dependencies.Attachment.ListAttachments(upstreamContext(ctx, request), &attachmentv1.ListAttachmentsRequest{
		Status: optionalString(status), Category: optionalString(category), Cursor: optionalString(cursor), Limit: optionalInt32(limit),
	})
	if err != nil {
		gatewaymiddleware.WriteAttachmentError(ctx, request, err)
		return
	}
	data, err := toMediaAttachmentListData(list, dependencies.EndpointOptions())
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCreateMediaAttachment(ctx context.Context, request *app.RequestContext) {
	idempotency, keyErr := idempotencyKey(request)
	var body createMediaAttachmentBody
	if keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil ||
		strings.TrimSpace(body.Filename) == "" || strings.TrimSpace(body.MediaType) == "" || body.SizeBytes <= 0 {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Attachment == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	upload, err := dependencies.Attachment.CreateAttachment(upstreamContext(ctx, request), &attachmentv1.CreateAttachmentRequest{
		Filename: body.Filename, MediaType: body.MediaType, SizeBytes: body.SizeBytes, IdempotencyKey: optionalString(idempotency),
	})
	if err != nil {
		gatewaymiddleware.WriteAttachmentError(ctx, request, err)
		return
	}
	data, err := toMediaAttachmentUploadData(upload, dependencies.EndpointOptions())
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Location", endpointURL(dependencies.EndpointOptions(), "/api/v1/attachments/"+url.PathEscape(data.Attachment.ID)))
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func handleGetMediaAttachment(ctx context.Context, request *app.RequestContext) {
	attachmentID, pathErr := pathUUID(request, "attachment_id")
	if pathErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Attachment == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	attachment, err := dependencies.Attachment.GetAttachment(upstreamContext(ctx, request), &attachmentv1.AttachmentIDRequest{AttachmentId: attachmentID})
	if err != nil {
		gatewaymiddleware.WriteAttachmentError(ctx, request, err)
		return
	}
	data, err := toMediaAttachmentData(attachment, dependencies.EndpointOptions())
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCompleteMediaAttachment(ctx context.Context, request *app.RequestContext) {
	attachmentID, pathErr := pathUUID(request, "attachment_id")
	var body completeMediaAttachmentBody
	if pathErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil || strings.TrimSpace(body.UploadID) == "" || len(body.Parts) == 0 {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	parts := make([]*attachmentv1.CompletePart, 0, len(body.Parts))
	seen := make(map[int32]struct{}, len(body.Parts))
	for _, part := range body.Parts {
		if part.PartNumber <= 0 || strings.TrimSpace(part.ETag) == "" {
			gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
			return
		}
		if _, exists := seen[part.PartNumber]; exists {
			gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
			return
		}
		seen[part.PartNumber] = struct{}{}
		parts = append(parts, &attachmentv1.CompletePart{PartNumber: part.PartNumber, Etag: strings.Trim(part.ETag, "\" ")})
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Attachment == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	attachment, err := dependencies.Attachment.CompleteAttachment(upstreamContext(ctx, request), &attachmentv1.CompleteAttachmentRequest{AttachmentId: attachmentID, UploadId: body.UploadID, Parts: parts})
	if err != nil {
		gatewaymiddleware.WriteAttachmentError(ctx, request, err)
		return
	}
	data, err := toMediaAttachmentData(attachment, dependencies.EndpointOptions())
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleDeleteMediaAttachment(ctx context.Context, request *app.RequestContext) {
	mutateMediaAttachment(ctx, request, false)
}

func handleRestoreMediaAttachment(ctx context.Context, request *app.RequestContext) {
	mutateMediaAttachment(ctx, request, true)
}

func mutateMediaAttachment(ctx context.Context, request *app.RequestContext, restore bool) {
	attachmentID, pathErr := pathUUID(request, "attachment_id")
	if pathErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok || dependencies.Attachment == nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	input := &attachmentv1.AttachmentIDRequest{AttachmentId: attachmentID}
	if !restore {
		if err := dependencies.Attachment.TrashAttachment(upstreamContext(ctx, request), input); err != nil {
			gatewaymiddleware.WriteAttachmentError(ctx, request, err)
			return
		}
		writeNoContent(ctx, request)
		return
	}
	attachment, err := dependencies.Attachment.RestoreAttachment(upstreamContext(ctx, request), input)
	if err != nil {
		gatewaymiddleware.WriteAttachmentError(ctx, request, err)
		return
	}
	data, err := toMediaAttachmentData(attachment, dependencies.EndpointOptions())
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

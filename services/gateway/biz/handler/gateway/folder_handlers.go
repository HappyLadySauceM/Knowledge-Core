package gateway

import (
	"context"
	"strings"

	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func handleListFolders(ctx context.Context, request *app.RequestContext) {
	values, queryErr := strictQuery(request, map[string]struct{}{"parent_id": {}})
	if queryErr != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	page, err := dependencies.Knowledge.ListFolders(upstreamContext(ctx, request), &knowledgev1.ListFoldersRequest{ParentId: optionalString(stringValue(queryPointer(values, "parent_id")))})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toFolderListData(page)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCreateFolder(ctx context.Context, request *app.RequestContext) {
	var body struct {
		Name     string  `json:"name"`
		ParentID *string `json:"parent_id,omitempty"`
	}
	idempotency, keyErr := idempotencyKey(request)
	if keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil || strings.TrimSpace(body.Name) == "" {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	folder, err := dependencies.Knowledge.CreateFolder(upstreamContext(ctx, request), &knowledgev1.CreateFolderRequest{Name: body.Name, ParentId: body.ParentID, IdempotencyKey: optionalString(idempotency)})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toFolderData(folder)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func handleUpdateFolder(ctx context.Context, request *app.RequestContext) {
	folderID, pathErr := pathUUID(request, "folder_id")
	revision, revisionErr := expectedRevision(request)
	var body struct {
		Name     *string `json:"name,omitempty"`
		ParentID *string `json:"parent_id,omitempty"`
	}
	if pathErr != nil || revisionErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil || (body.Name == nil && body.ParentID == nil) {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	if !ok {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	folder, err := dependencies.Knowledge.UpdateFolder(upstreamContext(ctx, request), &knowledgev1.UpdateFolderRequest{FolderId: folderID, ExpectedRevision: revision, Name: body.Name, ParentId: body.ParentID})
	if err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	data, err := toFolderData(folder)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleDeleteFolder(ctx context.Context, request *app.RequestContext) {
	folderID, pathErr := pathUUID(request, "folder_id")
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
	if err := dependencies.Knowledge.DeleteFolder(upstreamContext(ctx, request), &knowledgev1.DeleteFolderRequest{FolderId: folderID, ExpectedRevision: revision}); err != nil {
		gatewaymiddleware.WriteKnowledgeError(ctx, request, err)
		return
	}
	writeNoContent(ctx, request)
}

func toFolderListData(value *knowledgev1.FolderList) (*gatewaymodel.FolderListData, error) {
	if value == nil {
		return nil, errInvalidUpstream("knowledge folder list is nil")
	}
	items := make([]*gatewaymodel.FolderData, 0, len(value.Items))
	for _, item := range value.Items {
		converted, err := toFolderData(item)
		if err != nil {
			return nil, err
		}
		items = append(items, converted)
	}
	return &gatewaymodel.FolderListData{Items: items}, nil
}

func toFolderData(value *knowledgev1.Folder) (*gatewaymodel.FolderData, error) {
	if value == nil || !validUUIDv7(value.Id) || strings.TrimSpace(value.Name) == "" || value.Depth < 1 || value.Depth > 8 || value.Revision < 1 || !validRFC3339(value.CreatedAt) || !validRFC3339(value.UpdatedAt) {
		return nil, errInvalidUpstream("knowledge folder is incomplete")
	}
	if value.ParentId != nil && !validUUIDv7(*value.ParentId) {
		return nil, errInvalidUpstream("knowledge folder parent is invalid")
	}
	return &gatewaymodel.FolderData{ID: value.Id, ParentID: copyString(value.ParentId), Name: value.Name, Depth: value.Depth, Revision: value.Revision, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}, nil
}

func errInvalidUpstream(message string) error { return &upstreamValidationError{message: message} }

type upstreamValidationError struct{ message string }

func (e *upstreamValidationError) Error() string { return e.message }

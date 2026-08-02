package gateway

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	gatewayclient "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/client"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func handleListVersions(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	values, queryErr := strictQuery(request, map[string]struct{}{"cursor": {}, "limit": {}})
	cursor := stringValue(queryPointer(values, "cursor"))
	limit, limitErr := parseVersionLimit(queryPointer(values, "limit"))
	if pathErr != nil || queryErr != nil || limitErr != nil || len(cursor) > 1024 || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	token, tokenOK := gatewaymiddleware.AccessToken(request)
	if !ok || !tokenOK {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	page, err := dependencies.Collaboration.ListVersions(ctx, documentID, token, cursor, limit)
	if err != nil {
		gatewaymiddleware.WriteCollaborationError(ctx, request, err)
		return
	}
	data, err := toVersionPageData(page)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleCreateVersion(ctx context.Context, request *app.RequestContext) {
	documentID, pathErr := pathUUID(request, "document_id")
	idempotency, keyErr := idempotencyKey(request)
	var body createVersionBody
	if pathErr != nil || keyErr != nil || requireNoQuery(request) != nil || decodeJSONBody(request, &body) != nil ||
		!validVersionLabel(body.Label) {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	token, tokenOK := gatewaymiddleware.AccessToken(request)
	if !ok || !tokenOK {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	version, err := dependencies.Collaboration.CreateVersion(ctx, documentID, token, stringValue(body.Label), idempotency)
	if err != nil {
		gatewaymiddleware.WriteCollaborationError(ctx, request, err)
		return
	}
	data, err := toVersionData(version)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Location", endpointURL(dependencies.Endpoints, "/api/v1/studio/documents/"+url.PathEscape(documentID)+"/versions/"+url.PathEscape(data.ID)))
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func handleGetVersion(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	versionID, versionErr := pathUUID(request, "version_id")
	if documentErr != nil || versionErr != nil || requireNoQuery(request) != nil || requireNoBody(request) != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	token, tokenOK := gatewaymiddleware.AccessToken(request)
	if !ok || !tokenOK {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	detail, err := dependencies.Collaboration.GetVersion(ctx, documentID, versionID, token)
	if err != nil {
		gatewaymiddleware.WriteCollaborationError(ctx, request, err)
		return
	}
	data, err := toVersionDetailData(detail)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	writeJSON(ctx, request, consts.StatusOK, data)
}

func handleRestoreVersion(ctx context.Context, request *app.RequestContext) {
	documentID, documentErr := pathUUID(request, "document_id")
	versionID, versionErr := pathUUID(request, "version_id")
	idempotency, keyErr := idempotencyKey(request)
	var body restoreVersionBody
	if documentErr != nil || versionErr != nil || keyErr != nil || requireNoQuery(request) != nil ||
		decodeJSONBody(request, &body) != nil || body.ExpectedSequence < 0 {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidRequest)
		return
	}
	dependencies, ok := gatewaymiddleware.FromRequest(request)
	token, tokenOK := gatewaymiddleware.AccessToken(request)
	if !ok || !tokenOK {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInternal)
		return
	}
	version, err := dependencies.Collaboration.RestoreVersion(
		ctx, documentID, versionID, token, body.ExpectedSequence, idempotency,
	)
	if err != nil {
		gatewaymiddleware.WriteCollaborationError(ctx, request, err)
		return
	}
	data, err := toVersionData(version)
	if err != nil {
		gatewaymiddleware.WriteError(ctx, request, gatewaymiddleware.ErrInvalidUpstreamResponse)
		return
	}
	request.Header("Location", endpointURL(dependencies.Endpoints, "/api/v1/studio/documents/"+url.PathEscape(documentID)+"/versions/"+url.PathEscape(data.ID)))
	writeJSON(ctx, request, consts.StatusCreated, data)
}

func parseVersionLimit(value *string) (int32, error) {
	if value == nil || *value == "" {
		return 0, nil
	}
	if !positiveDecimalPattern.MatchString(*value) {
		return 0, errors.New("limit must be an integer between 1 and 100")
	}
	parsed, err := strconv.ParseInt(*value, 10, 32)
	if err != nil || parsed > 100 {
		return 0, errors.New("limit must be an integer between 1 and 100")
	}
	return int32(parsed), nil
}

func validVersionLabel(value *string) bool {
	if value == nil {
		return true
	}
	return strings.TrimSpace(*value) == *value && len([]rune(*value)) >= 1 && len([]rune(*value)) <= 200 && !containsControl(*value)
}

func toVersionPageData(value *gatewayclient.VersionPage) (*gatewaymodel.VersionPageData, error) {
	if value == nil || value.Page.HasMore != (value.Page.NextCursor != nil) {
		return nil, errors.New("collaboration version page is incomplete")
	}
	if value.Page.NextCursor != nil && (len(*value.Page.NextCursor) == 0 || len(*value.Page.NextCursor) > 1024) {
		return nil, errors.New("collaboration version cursor is invalid")
	}
	items := make([]*gatewaymodel.VersionData, 0, len(value.Items))
	for index := range value.Items {
		converted, err := toVersionData(&value.Items[index])
		if err != nil {
			return nil, err
		}
		items = append(items, converted)
	}
	return &gatewaymodel.VersionPageData{
		Items: items,
		Page:  &gatewaymodel.PageInfoData{NextCursor: copyString(value.Page.NextCursor), HasMore: value.Page.HasMore},
	}, nil
}

func toVersionDetailData(value *gatewayclient.VersionDetail) (*gatewaymodel.VersionDetailData, error) {
	if value == nil || value.Content == nil {
		return nil, errors.New("collaboration version detail is incomplete")
	}
	version, err := toVersionData(&value.Version)
	if err != nil {
		return nil, err
	}
	payload, err := jsoncodec.Marshal(value.Content)
	if err != nil {
		return nil, err
	}
	var content gatewaymodel.RichTextDocumentData
	if err := jsoncodec.Unmarshal(payload, &content); err != nil || !validPublicRichTextDocument(&content, 0) {
		return nil, errors.New("collaboration version content is invalid")
	}
	if content.Content == nil {
		content.Content = make([]*gatewaymodel.RichTextNodeData, 0)
	}
	return &gatewaymodel.VersionDetailData{Version: version, Content: &content, PlainText: value.PlainText}, nil
}

func toVersionData(value *gatewayclient.Version) (*gatewaymodel.VersionData, error) {
	if value == nil || !validUUIDv7(value.ID) || !validUUIDv7(value.DocumentID) || value.Sequence < 0 ||
		(value.Kind != "manual" && value.Kind != "automatic" && value.Kind != "restoration") ||
		!validVersionLabel(value.Label) || value.CreatedBy.ID <= 0 || strings.TrimSpace(value.CreatedBy.Username) == "" ||
		!validRFC3339(value.CreatedAt) {
		return nil, errors.New("collaboration version is incomplete")
	}
	return &gatewaymodel.VersionData{
		ID: value.ID, DocumentID: value.DocumentID, Sequence: value.Sequence, Kind: value.Kind,
		Label: copyString(value.Label), CreatedBy: &gatewaymodel.PublicUserData{
			ID: strconv.FormatInt(value.CreatedBy.ID, 10), Username: value.CreatedBy.Username, Avatar: value.CreatedBy.Avatar,
		}, CreatedAt: value.CreatedAt,
	}, nil
}

func validPublicRichTextDocument(value *gatewaymodel.RichTextDocumentData, depth int) bool {
	if value == nil || value.Type != "doc" || depth > maximumRichTextDepth {
		return false
	}
	for _, node := range value.Content {
		if !validPublicRichTextNode(node, depth+1) {
			return false
		}
	}
	return true
}

func validPublicRichTextNode(value *gatewaymodel.RichTextNodeData, depth int) bool {
	if value == nil || strings.TrimSpace(value.Type) == "" || depth > maximumRichTextDepth {
		return false
	}
	for _, child := range value.Content {
		if !validPublicRichTextNode(child, depth+1) {
			return false
		}
	}
	for _, mark := range value.Marks {
		if mark == nil || strings.TrimSpace(mark.Type) == "" {
			return false
		}
	}
	return true
}

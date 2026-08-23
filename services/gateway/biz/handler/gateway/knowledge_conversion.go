package gateway

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	knowledgev1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/knowledge"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	"github.com/google/uuid"
)

const maximumRichTextDepth = 100

func toDocumentPageData(value *knowledgev1.DocumentPage) (*gatewaymodel.DocumentPageData, error) {
	if value == nil || value.Page == nil {
		return nil, errors.New("knowledge document page is incomplete")
	}
	if value.Page.HasMore != (value.Page.NextCursor != nil) {
		return nil, errors.New("knowledge document page cursor is inconsistent")
	}
	if value.Page.NextCursor != nil && (len(*value.Page.NextCursor) == 0 || len(*value.Page.NextCursor) > 1024) {
		return nil, errors.New("knowledge document page cursor is invalid")
	}
	items := make([]*gatewaymodel.DocumentData, 0, len(value.Items))
	for _, document := range value.Items {
		converted, err := toDocumentData(document)
		if err != nil {
			return nil, err
		}
		items = append(items, converted)
	}
	return &gatewaymodel.DocumentPageData{
		Items: items,
		Page:  &gatewaymodel.PageInfoData{NextCursor: copyString(value.Page.NextCursor), HasMore: value.Page.HasMore},
	}, nil
}

func toDocumentData(value *knowledgev1.Document) (*gatewaymodel.DocumentData, error) {
	if value == nil || !validUUIDv7(value.Id) || strings.TrimSpace(value.Title) == "" ||
		!slugPattern.MatchString(value.Slug) || value.MetadataRevision <= 0 || value.ContentRevision < 0 ||
		!validDocumentAccess(value.Access) || !validRFC3339(value.CreatedAt) || !validRFC3339(value.UpdatedAt) ||
		!validOptionalTime(value.PublishedAt) || !validOptionalTime(value.DeletedAt) || !validOptionalTime(value.ProjectedAt) {
		return nil, errors.New("knowledge document is incomplete")
	}
	owner, err := toPublicUserData(value.Owner)
	if err != nil {
		return nil, err
	}
	return &gatewaymodel.DocumentData{
		ID: value.Id, Title: value.Title, Summary: value.Summary, Slug: value.Slug, Owner: owner,
		Access: value.Access, Published: value.Published, MetadataRevision: value.MetadataRevision,
		ContentRevision: value.ContentRevision, PublishedAt: copyString(value.PublishedAt), DeletedAt: copyString(value.DeletedAt),
		ProjectedAt: copyString(value.ProjectedAt), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		Language: copyString(value.Language), Tags: append([]string(nil), value.Tags...), FolderID: copyString(value.FolderId),
	}, nil
}

func toDocumentDetailData(value *knowledgev1.DocumentDetail, endpoints config.EndpointOptions) (*gatewaymodel.DocumentDetailData, error) {
	if value == nil || value.Content == nil {
		return nil, errors.New("knowledge document detail is incomplete")
	}
	document, err := toDocumentData(value.Document)
	if err != nil {
		return nil, err
	}
	content, err := toRichTextDocumentData(value.Content)
	if err != nil {
		return nil, err
	}
	attachments := make([]*gatewaymodel.AttachmentData, 0, len(value.Attachments))
	for _, attachment := range value.Attachments {
		converted, conversionErr := toAttachmentData(attachment, endpoints)
		if conversionErr != nil {
			return nil, conversionErr
		}
		attachments = append(attachments, converted)
	}
	return &gatewaymodel.DocumentDetailData{
		Document: document, Content: content, PlainText: value.PlainText, Attachments: attachments,
	}, nil
}

func toMemberListData(value *knowledgev1.MemberList) (*gatewaymodel.MemberListData, error) {
	if value == nil {
		return nil, errors.New("knowledge member list is nil")
	}
	items := make([]*gatewaymodel.MemberData, 0, len(value.Items))
	for _, member := range value.Items {
		converted, err := toMemberData(member)
		if err != nil {
			return nil, err
		}
		items = append(items, converted)
	}
	return &gatewaymodel.MemberListData{Items: items}, nil
}

func toMemberData(value *knowledgev1.Member) (*gatewaymodel.MemberData, error) {
	if value == nil || value.Revision <= 0 || (value.Role != "viewer" && value.Role != "editor") ||
		!validRFC3339(value.CreatedAt) || !validRFC3339(value.UpdatedAt) {
		return nil, errors.New("knowledge member is incomplete")
	}
	user, err := toPublicUserData(value.User)
	if err != nil {
		return nil, err
	}
	return &gatewaymodel.MemberData{
		User: user, Role: value.Role, Revision: value.Revision, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}, nil
}

func toAttachmentListData(value *knowledgev1.AttachmentList, endpoints config.EndpointOptions) (*gatewaymodel.AttachmentListData, error) {
	if value == nil {
		return nil, errors.New("knowledge attachment list is nil")
	}
	items := make([]*gatewaymodel.AttachmentData, 0, len(value.Items))
	for _, attachment := range value.Items {
		converted, err := toAttachmentData(attachment, endpoints)
		if err != nil {
			return nil, err
		}
		items = append(items, converted)
	}
	return &gatewaymodel.AttachmentListData{Items: items}, nil
}

func toAttachmentData(value *knowledgev1.Attachment, endpoints config.EndpointOptions) (*gatewaymodel.AttachmentData, error) {
	if value == nil || !validUUIDv7(value.Id) || !validUUIDv7(value.DocumentId) || strings.TrimSpace(value.Filename) == "" ||
		strings.TrimSpace(value.MediaType) == "" || value.SizeBytes <= 0 || !validAttachmentStatus(value.Status) ||
		!validRFC3339(value.CreatedAt) {
		return nil, errors.New("knowledge attachment is incomplete")
	}
	return &gatewaymodel.AttachmentData{
		ID: value.Id, DocumentID: value.DocumentId, Filename: value.Filename, MediaType: value.MediaType,
		SizeBytes: value.SizeBytes, Status: value.Status,
		ContentURL: endpointURL(endpoints, "/api/v1/attachments/"+url.PathEscape(value.Id)+"/content"), CreatedAt: value.CreatedAt,
	}, nil
}

func toAttachmentUploadData(value *knowledgev1.AttachmentUpload, endpoints config.EndpointOptions) (*gatewaymodel.AttachmentUploadData, error) {
	if value == nil || !validRedirectURL(value.UploadUrl) || !validRFC3339(value.ExpiresAt) || value.RequiredHeaders == nil {
		return nil, errors.New("knowledge attachment upload is incomplete")
	}
	attachment, err := toAttachmentData(value.Attachment, endpoints)
	if err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(value.RequiredHeaders))
	for name, headerValue := range value.RequiredHeaders {
		if !validHeaderName(name) || strings.ContainsAny(headerValue, "\r\n") {
			return nil, errors.New("knowledge attachment upload contains invalid headers")
		}
		headers[name] = headerValue
	}
	return &gatewaymodel.AttachmentUploadData{
		Attachment: attachment, UploadURL: value.UploadUrl, RequiredHeaders: headers, ExpiresAt: value.ExpiresAt,
	}, nil
}

func validHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", char) {
			return false
		}
	}
	return true
}

func toPublicUserData(value *knowledgev1.PublicUser) (*gatewaymodel.PublicUserData, error) {
	if value == nil || value.Id <= 0 || strings.TrimSpace(value.Username) == "" {
		return nil, errors.New("knowledge public user is incomplete")
	}
	return &gatewaymodel.PublicUserData{
		ID: strconv.FormatInt(value.Id, 10), Username: value.Username, Avatar: value.Avatar,
	}, nil
}

func toRichTextDocumentData(value *knowledgev1.RichTextDocument) (*gatewaymodel.RichTextDocumentData, error) {
	if value == nil || value.Type != "doc" {
		return nil, errors.New("knowledge rich-text document is invalid")
	}
	content := make([]*gatewaymodel.RichTextNodeData, 0, len(value.Content))
	for _, node := range value.Content {
		converted, err := toRichTextNodeData(node, 1)
		if err != nil {
			return nil, err
		}
		content = append(content, converted)
	}
	return &gatewaymodel.RichTextDocumentData{Type: value.Type, Content: content}, nil
}

func toRichTextNodeData(value *knowledgev1.RichTextNode, depth int) (*gatewaymodel.RichTextNodeData, error) {
	if value == nil || strings.TrimSpace(value.Type) == "" || depth > maximumRichTextDepth {
		return nil, errors.New("knowledge rich-text node is invalid")
	}
	result := &gatewaymodel.RichTextNodeData{Type: value.Type, Text: copyString(value.Text)}
	if value.Attrs != nil {
		result.Attrs = toRichTextAttrsData(value.Attrs)
	}
	if value.Content != nil {
		result.Content = make([]*gatewaymodel.RichTextNodeData, 0, len(value.Content))
		for _, child := range value.Content {
			converted, err := toRichTextNodeData(child, depth+1)
			if err != nil {
				return nil, err
			}
			result.Content = append(result.Content, converted)
		}
	}
	if value.Marks != nil {
		result.Marks = make([]*gatewaymodel.RichTextMarkData, 0, len(value.Marks))
		for _, mark := range value.Marks {
			if mark == nil || strings.TrimSpace(mark.Type) == "" {
				return nil, errors.New("knowledge rich-text mark is invalid")
			}
			converted := &gatewaymodel.RichTextMarkData{Type: mark.Type}
			if mark.Attrs != nil {
				converted.Attrs = toRichTextAttrsData(mark.Attrs)
			}
			result.Marks = append(result.Marks, converted)
		}
	}
	return result, nil
}

func toRichTextAttrsData(value *knowledgev1.RichTextAttrs) *gatewaymodel.RichTextAttrsData {
	if value == nil {
		return nil
	}
	return &gatewaymodel.RichTextAttrsData{
		Level: copyInt32(value.Level), Start: copyInt32(value.Start), Checked: copyBool(value.Checked),
		Language: copyString(value.Language), Href: copyString(value.Href), AttachmentID: copyString(value.AttachmentId),
		Alt: copyString(value.Alt), Title: copyString(value.Title), TextAlign: copyString(value.TextAlign),
		Colspan: copyInt32(value.Colspan), Rowspan: copyInt32(value.Rowspan), Colwidth: append([]int32(nil), value.Colwidth...),
	}
}

func validUUIDv7(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.Version() == 7 && parsed.String() == value
}

func validOptionalTime(value *string) bool {
	return value == nil || validRFC3339(*value)
}

func validDocumentAccess(value string) bool {
	return value == "viewer" || value == "editor" || value == "owner"
}

func validAttachmentStatus(value string) bool {
	switch value {
	case "pending_upload", "scanning", "ready", "rejected", "deleting":
		return true
	default:
		return false
	}
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func copyBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

package gateway

import (
	"errors"
	"net/url"
	"strings"

	attachmentv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/attachment"
	gatewaymodel "github.com/HappyLadySauce/Knowledge-Core/services/gateway/biz/model/gateway"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
)

func toMediaAttachmentData(value *attachmentv1.Attachment, endpoints config.EndpointOptions) (*gatewaymodel.MediaAttachmentData, error) {
	if value == nil || !validUUIDv7(value.Id) || value.OwnerId <= 0 || strings.TrimSpace(value.Filename) == "" ||
		strings.TrimSpace(value.MediaType) == "" || strings.TrimSpace(value.Category) == "" || value.SizeBytes <= 0 ||
		value.PartSize <= 0 || value.PartCount <= 0 || !validAttachmentServiceStatus(value.Status) || !validRFC3339(value.CreatedAt) {
		return nil, errors.New("attachment response is incomplete")
	}
	return &gatewaymodel.MediaAttachmentData{
		ID: value.Id, OwnerID: value.OwnerId, Filename: value.Filename, MediaType: value.MediaType,
		Category: value.Category, SizeBytes: value.SizeBytes, Sha256: value.Sha256, Status: value.Status,
		PartSize: value.PartSize, PartCount: value.PartCount, CreatedAt: value.CreatedAt,
		DetectedType: copyString(value.DetectedType),
		ContentURL:   endpointURL(endpoints, "/api/v1/attachments/"+url.PathEscape(value.Id)+"/content"),
	}, nil
}

func toMediaAttachmentListData(value *attachmentv1.AttachmentList, endpoints config.EndpointOptions) (*gatewaymodel.MediaAttachmentListData, error) {
	if value == nil {
		return nil, errors.New("attachment list is nil")
	}
	items := make([]*gatewaymodel.MediaAttachmentData, 0, len(value.Items))
	for _, item := range value.Items {
		converted, err := toMediaAttachmentData(item, endpoints)
		if err != nil {
			return nil, err
		}
		items = append(items, converted)
	}
	return &gatewaymodel.MediaAttachmentListData{Items: items}, nil
}

func toMediaAttachmentUploadData(value *attachmentv1.AttachmentUpload, endpoints config.EndpointOptions) (*gatewaymodel.MediaAttachmentUploadData, error) {
	if value == nil || !validRFC3339(value.ExpiresAt) || strings.TrimSpace(value.UploadId) == "" {
		return nil, errors.New("attachment upload response is incomplete")
	}
	attachment, err := toMediaAttachmentData(value.Attachment, endpoints)
	if err != nil {
		return nil, err
	}
	parts := make([]*gatewaymodel.MediaAttachmentPartData, 0, len(value.Parts))
	for _, part := range value.Parts {
		if part == nil || part.PartNumber <= 0 || !validRFC3339(part.ExpiresAt) || !validRedirectURL(part.Url) {
			return nil, errors.New("attachment upload part is invalid")
		}
		parts = append(parts, &gatewaymodel.MediaAttachmentPartData{PartNumber: part.PartNumber, URL: part.Url, ExpiresAt: part.ExpiresAt})
	}
	if len(parts) != int(attachment.PartCount) {
		return nil, errors.New("attachment upload part count is invalid")
	}
	return &gatewaymodel.MediaAttachmentUploadData{Attachment: attachment, UploadID: value.UploadId, Parts: parts, ExpiresAt: value.ExpiresAt}, nil
}

func validAttachmentServiceStatus(value string) bool {
	switch value {
	case "pending_upload", "scanning", "scan_parked", "ready", "rejected", "trashed", "deleted":
		return true
	default:
		return false
	}
}

package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	StatusPendingUpload       = "pending_upload"
	StatusScanning            = "scanning"
	StatusReady               = "ready"
	StatusRejected            = "rejected"
	StatusTrashed             = "trashed"
	StatusDeleted             = "deleted"
	CategoryImage             = "image"
	CategoryAudio             = "audio"
	CategoryVideo             = "video"
	CategoryDocument          = "document"
	CategoryArchive           = "archive"
	CategoryFile              = "file"
	PartSize            int64 = 16 << 20
	MaxParts            int32 = 64
	MaxUserBytes        int64 = 10 << 30
)

var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

var allowed = map[string]string{"image/jpeg": CategoryImage, "image/png": CategoryImage, "image/gif": CategoryImage, "image/webp": CategoryImage, "image/avif": CategoryImage, "audio/mpeg": CategoryAudio, "audio/ogg": CategoryAudio, "audio/wav": CategoryAudio, "audio/webm": CategoryAudio, "video/mp4": CategoryVideo, "video/webm": CategoryVideo, "video/quicktime": CategoryVideo, "application/pdf": CategoryDocument, "application/zip": CategoryArchive, "application/x-7z-compressed": CategoryArchive, "application/x-rar-compressed": CategoryArchive, "text/plain": CategoryFile, "text/markdown": CategoryFile, "application/json": CategoryFile, "application/vnd.openxmlformats-officedocument.wordprocessingml.document": CategoryDocument, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": CategoryDocument, "application/vnd.openxmlformats-officedocument.presentationml.presentation": CategoryDocument}

type Attachment struct {
	ID                                                string
	OwnerID                                           int64
	Filename, MediaType, Category                     string
	SizeBytes                                         int64
	SHA256, DetectedType, ObjectKey, UploadID, Status string
	PartSize                                          int64
	PartCount                                         int32
	CreatedAt, UpdatedAt                              time.Time
}
type ScanResult struct {
	Clean        bool
	SHA256       string
	Size         int64
	DetectedType string
}

func NewID() string { return uuid.NewString() }
func Validate(filename, mediaType string, size int64) (string, error) {
	filename = strings.TrimSpace(filename)
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if filename == "" || len(filename) > 255 {
		return "", errors.New("filename is invalid")
	}
	if filepath.Base(filename) != filename {
		return "", errors.New("filename must not contain a path")
	}
	category, ok := allowed[mediaType]
	if !ok {
		return "", errors.New("media type is not allowed")
	}
	if size <= 0 || size > 1<<30 {
		return "", errors.New("attachment size must be between 1 byte and 1 GiB")
	}
	return category, nil
}
func ValidateHash(hash string) error {
	if !shaPattern.MatchString(hash) {
		return errors.New("sha256 must be a 64 character hex string")
	}
	return nil
}
func ObjectKey(id string) string { return fmt.Sprintf("objects/%s/%s", id[:2], id) }

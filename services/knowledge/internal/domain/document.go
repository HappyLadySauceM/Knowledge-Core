package domain

import (
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/google/uuid"
)

const (
	AccessNone   = "none"
	AccessViewer = "viewer"
	AccessEditor = "editor"
	AccessOwner  = "owner"

	AttachmentPendingUpload = "pending_upload"
	AttachmentScanning      = "scanning"
	AttachmentReady         = "ready"
	AttachmentRejected      = "rejected"
	AttachmentDeleting      = "deleting"

	MaxImageBytes          int64 = 10 << 20
	MaxFileBytes           int64 = 50 << 20
	MaxDocumentBytes       int64 = 1 << 30
	MaxUserAttachmentBytes int64 = 10 << 30
	MaxProjectionBytes           = 16 << 20
)

var (
	slugPattern           = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	sha256Pattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	idempotencyKeyPattern = regexp.MustCompile(`^[\x21-\x7e]{1,128}$`)
)

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("validate %s: %s", e.Field, e.Reason) }

type PublicUser struct {
	ID       int64
	Username string
	Avatar   string
}

type Document struct {
	ID                 string
	Title              string
	Summary            string
	Slug               string
	Owner              PublicUser
	Access             string
	Published          bool
	MetadataRevision   int64
	ContentRevision    int64
	PermissionRevision int64
	PublishedAt        *time.Time
	DeletedAt          *time.Time
	PurgeAfter         *time.Time
	ProjectedAt        *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Projection struct {
	DocumentID  string
	Sequence    int64
	Content     []byte
	PlainText   string
	ProjectedAt time.Time
}

type Member struct {
	DocumentID string
	User       PublicUser
	Role       string
	Revision   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Attachment struct {
	ID            string
	DocumentID    string
	UploaderID    int64
	Filename      string
	DeclaredType  string
	DetectedType  string
	SizeBytes     int64
	SHA256        string
	ObjectKey     string
	Status        string
	FailureReason string
	UploadExpires time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type AttachmentUpload struct {
	Attachment      *Attachment
	URL             string
	RequiredHeaders map[string]string
	ExpiresAt       time.Time
}

type UploadTarget struct {
	URL             string
	RequiredHeaders map[string]string
	ExpiresAt       time.Time
}

type AttachmentContent struct {
	URL       string
	ExpiresAt time.Time
}

type ScanJob struct {
	Attachment Attachment
	Attempts   int
}

type ScanResult struct {
	Clean        bool
	DetectedType string
}

type OutboxMessage struct {
	ID       string
	Subject  string
	Payload  []byte
	Attempts int
}

func NewID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7: %w", err)
	}
	return id.String(), nil
}

func ValidateID(field, value string) error {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil || id.Version() != 7 {
		return &ValidationError{Field: field, Reason: "must be a UUIDv7"}
	}
	return nil
}

func ValidateTitle(value string) error {
	length := len([]rune(strings.TrimSpace(value)))
	if length < 1 || length > 200 {
		return &ValidationError{Field: "title", Reason: "must contain between 1 and 200 characters"}
	}
	return nil
}

func ValidateSummary(value string) error {
	if len([]rune(strings.TrimSpace(value))) > 1000 {
		return &ValidationError{Field: "summary", Reason: "must contain at most 1000 characters"}
	}
	return nil
}

func NormalizeSlug(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 80 || !slugPattern.MatchString(value) {
		return "", &ValidationError{Field: "slug", Reason: "must be 3-80 lowercase ASCII letters, digits, or single hyphens"}
	}
	switch value {
	case "api", "admin", "studio", "trash", "health", "attachments":
		return "", &ValidationError{Field: "slug", Reason: "is reserved"}
	}
	return value, nil
}

func SlugFromTitle(title, id string) string {
	var builder strings.Builder
	lastHyphen := false
	for _, char := range strings.ToLower(strings.TrimSpace(title)) {
		allowed := char >= 'a' && char <= 'z' || char >= '0' && char <= '9'
		if allowed {
			builder.WriteRune(char)
			lastHyphen = false
			continue
		}
		if builder.Len() > 0 && !lastHyphen {
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	base := strings.Trim(builder.String(), "-")
	if len(base) > 60 {
		base = strings.TrimRight(base[:60], "-")
	}
	if len(base) < 3 {
		base = "document"
	}
	compact := strings.ReplaceAll(id, "-", "")
	if len(compact) > 10 {
		compact = compact[:10]
	}
	return base + "-" + compact
}

func ValidateRole(role string) error {
	if role != AccessViewer && role != AccessEditor {
		return &ValidationError{Field: "role", Reason: "must be viewer or editor"}
	}
	return nil
}

func CanRead(access string) bool {
	return access == AccessOwner || access == AccessEditor || access == AccessViewer
}

func CanEdit(access string) bool { return access == AccessOwner || access == AccessEditor }

func CanManageMembers(access string) bool { return access == AccessOwner }

func ValidatePage(limit int32, cursor, query string) error {
	var joined error
	if limit < 0 || limit > 100 {
		joined = errors.Join(joined, &ValidationError{Field: "limit", Reason: "must be between 1 and 100"})
	}
	if len(cursor) > 1024 {
		joined = errors.Join(joined, &ValidationError{Field: "cursor", Reason: "is too long"})
	}
	if len([]rune(query)) > 200 {
		joined = errors.Join(joined, &ValidationError{Field: "q", Reason: "must contain at most 200 characters"})
	}
	return joined
}

func ValidateIdempotencyKey(value string) error {
	if value == "" {
		return nil
	}
	if value != strings.TrimSpace(value) || !idempotencyKeyPattern.MatchString(value) {
		return &ValidationError{Field: "idempotency_key", Reason: "must contain 1-128 visible ASCII characters"}
	}
	return nil
}

func ValidateAttachment(filename, mediaType string, sizeBytes int64, checksum string) error {
	filename = strings.TrimSpace(filename)
	mediaType = strings.TrimSpace(mediaType)
	var joined error
	if len([]rune(filename)) < 1 || len([]rune(filename)) > 255 || filename != filepath.Base(filename) || strings.ContainsAny(filename, `/\\`) {
		joined = errors.Join(joined, &ValidationError{Field: "filename", Reason: "must be a basename containing 1-255 characters"})
	} else {
		for _, value := range filename {
			if unicode.IsControl(value) {
				joined = errors.Join(joined, &ValidationError{Field: "filename", Reason: "must not contain control characters"})
				break
			}
		}
	}
	parsedType, _, err := mime.ParseMediaType(mediaType)
	if err != nil || parsedType != mediaType || len(mediaType) > 127 {
		joined = errors.Join(joined, &ValidationError{Field: "media_type", Reason: "must be a canonical media type"})
	}
	maximum := MaxFileBytes
	if strings.HasPrefix(mediaType, "image/") {
		maximum = MaxImageBytes
	}
	if sizeBytes <= 0 || sizeBytes > maximum {
		joined = errors.Join(joined, &ValidationError{Field: "size_bytes", Reason: fmt.Sprintf("must be between 1 and %d", maximum)})
	}
	if !sha256Pattern.MatchString(checksum) {
		joined = errors.Join(joined, &ValidationError{Field: "sha256", Reason: "must be 64 lowercase hexadecimal characters"})
	}
	return joined
}

func ValidateProjection(content []byte, plainText string) error {
	if len(content) == 0 || len(content) > MaxProjectionBytes || !jsoncodec.Valid(content) {
		return &ValidationError{Field: "content", Reason: "must be a valid JSON document within the projection size limit"}
	}
	if int64(len(plainText)) > MaxDocumentBytes {
		return &ValidationError{Field: "plain_text", Reason: "exceeds the document size limit"}
	}
	return nil
}

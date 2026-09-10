package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/google/uuid"
)

const (
	AccessNone   = "none"
	AccessViewer = "viewer"
	AccessEditor = "editor"
	AccessOwner  = "owner"

	PublicationDraft           = "draft"
	PublicationPublishing      = "publishing"
	PublicationPublished       = "published"
	PublicationPublishFailed   = "publish_failed"
	PublicationUnpublishing    = "unpublishing"
	PublicationUnpublishFailed = "unpublish_failed"

	MaxDocumentBytes   int64 = 1 << 30
	MaxProjectionBytes       = 16 << 20
)

var (
	slugPattern           = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
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
	ID                    string
	Title                 string
	Summary               string
	Slug                  string
	Language              string
	Tags                  []string
	FolderID              *string
	Owner                 PublicUser
	Access                string
	Published             bool
	PublicationStatus     string
	PublicationError      *string
	PublicationGeneration int64
	MetadataRevision      int64
	ContentRevision       int64
	PermissionRevision    int64
	PublishedAt           *time.Time
	DeletedAt             *time.Time
	PurgeAfter            *time.Time
	ProjectedAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type PublicationSnapshot struct {
	DocumentID      string
	VersionID       *string
	VersionSequence int64
	Title           string
	Summary         string
	Slug            string
	Language        string
	Tags            []string
	Owner           PublicUser
	Content         RichTextDocument
	PlainText       string
	PublishedAt     time.Time
	UpdatedAt       time.Time
}

type Folder struct {
	ID        string
	ParentID  *string
	Name      string
	Depth     int32
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
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

type PublicationReferenceJob struct {
	ID            string
	DocumentID    string
	OwnerID       int64
	Generation    int64
	Action        string
	AttachmentIDs []string
	State         string
	Attempts      int
	Headers       map[string]string
}

type OutboxMessage struct {
	ID       string
	Subject  string
	Payload  []byte
	Headers  map[string]string
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

func NormalizeTags(values []string) ([]string, error) {
	if len(values) > 20 {
		return nil, &ValidationError{Field: "tags", Reason: "must contain at most 20 tags"}
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if value == "" || len([]rune(value)) > 64 {
			return nil, &ValidationError{Field: "tags", Reason: "each tag must contain 1-64 characters"}
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result, nil
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

func ValidateProjection(content []byte, plainText string) error {
	if len(content) == 0 || len(content) > MaxProjectionBytes || !jsoncodec.Valid(content) {
		return &ValidationError{Field: "content", Reason: "must be a valid JSON document within the projection size limit"}
	}
	if int64(len(plainText)) > MaxDocumentBytes {
		return &ValidationError{Field: "plain_text", Reason: "exceeds the document size limit"}
	}
	return nil
}

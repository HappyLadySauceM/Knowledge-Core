package gateway

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	jsoncodec "github.com/HappyLadySauce/Knowledge-Core/pkg/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/config"
	gatewaymiddleware "github.com/HappyLadySauce/Knowledge-Core/services/gateway/internal/middleware"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
)

var (
	positiveDecimalPattern = regexp.MustCompile(`^[1-9][0-9]*$`)
	slugPattern            = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type listInput struct {
	query       *string
	cursor      *string
	limit       *int32
	access      *string
	publication *string
}

type createDocumentBody struct {
	Title   string  `json:"title"`
	Summary *string `json:"summary,omitempty"`
	Slug    *string `json:"slug,omitempty"`
}

type updateDocumentBody struct {
	Title   *string `json:"title,omitempty"`
	Summary *string `json:"summary,omitempty"`
	Slug    *string `json:"slug,omitempty"`
}

type addMemberBody struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

type updateMemberBody struct {
	Role string `json:"role"`
}

type createVersionBody struct {
	Label *string `json:"label,omitempty"`
}

type restoreVersionBody struct {
	ExpectedSequence int64 `json:"expected_sequence"`
}

type createAttachmentBody struct {
	Filename  string `json:"filename"`
	MediaType string `json:"media_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

func decodeJSONBody(request *app.RequestContext, target any) error {
	if request == nil || target == nil {
		return errors.New("request and target are required")
	}
	contentType, present, err := singleHeader(request, "Content-Type")
	if err != nil || !present {
		return errors.New("Content-Type must occur once")
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	body := request.Request.Body()
	if len(body) == 0 {
		return errors.New("request body is required")
	}
	return jsoncodec.Unmarshal(body, target)
}

func requireNoBody(request *app.RequestContext) error {
	if request == nil {
		return errors.New("request is required")
	}
	if len(request.Request.Body()) != 0 {
		return errors.New("request body must be empty")
	}
	return nil
}

func decodeListInput(request *app.RequestContext, studio bool) (listInput, error) {
	allowed := map[string]struct{}{"q": {}, "cursor": {}, "limit": {}}
	if studio {
		allowed["access"] = struct{}{}
		allowed["publication"] = struct{}{}
	}
	values, err := strictQuery(request, allowed)
	if err != nil {
		return listInput{}, err
	}
	result := listInput{
		query: queryPointer(values, "q"), cursor: queryPointer(values, "cursor"),
		access: queryPointer(values, "access"), publication: queryPointer(values, "publication"),
	}
	if result.query != nil {
		if !utf8.ValidString(*result.query) || len([]rune(*result.query)) > 200 || containsControl(*result.query) {
			return listInput{}, errors.New("q must contain at most 200 non-control characters")
		}
	}
	if result.cursor != nil && (len(*result.cursor) > 1024 || containsControl(*result.cursor)) {
		return listInput{}, errors.New("cursor is invalid")
	}
	if raw := queryPointer(values, "limit"); raw != nil {
		if !positiveDecimalPattern.MatchString(*raw) {
			return listInput{}, errors.New("limit must be an integer between 1 and 100")
		}
		parsed, parseErr := strconv.ParseInt(*raw, 10, 32)
		if parseErr != nil || parsed > 100 {
			return listInput{}, errors.New("limit must be an integer between 1 and 100")
		}
		value := int32(parsed)
		result.limit = &value
	}
	if result.access != nil && *result.access != "owner" && *result.access != "shared" {
		return listInput{}, errors.New("access must be owner or shared")
	}
	if result.publication != nil && *result.publication != "published" && *result.publication != "draft" {
		return listInput{}, errors.New("publication must be published or draft")
	}
	return result, nil
}

func strictQuery(request *app.RequestContext, allowed map[string]struct{}) (url.Values, error) {
	if request == nil {
		return nil, errors.New("request is required")
	}
	raw := string(request.URI().QueryString())
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("parse query: %w", err)
	}
	for name, entries := range values {
		if _, ok := allowed[name]; !ok || name == "" {
			return nil, fmt.Errorf("unknown query parameter %q", name)
		}
		if len(entries) != 1 {
			return nil, fmt.Errorf("query parameter %q must occur once", name)
		}
	}
	return values, nil
}

func requireNoQuery(request *app.RequestContext) error {
	_, err := strictQuery(request, map[string]struct{}{})
	return err
}

func queryPointer(values url.Values, name string) *string {
	entries, ok := values[name]
	if !ok || len(entries) != 1 {
		return nil
	}
	value := entries[0]
	return &value
}

func singleHeader(request *app.RequestContext, name string) (string, bool, error) {
	if request == nil {
		return "", false, errors.New("request is required")
	}
	values := request.Request.Header.PeekAll(name)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", true, fmt.Errorf("%s must occur once", name)
	}
	return string(values[0]), true, nil
}

func idempotencyKey(request *app.RequestContext) (string, error) {
	value, present, err := singleHeader(request, "Idempotency-Key")
	if err != nil || !present {
		return "", err
	}
	if len(value) < 1 || len(value) > 128 || strings.TrimSpace(value) != value {
		return "", errors.New("Idempotency-Key must contain 1-128 visible ASCII characters")
	}
	for _, char := range value {
		if char < 0x21 || char > 0x7e {
			return "", errors.New("Idempotency-Key must contain 1-128 visible ASCII characters")
		}
	}
	return value, nil
}

func expectedRevision(request *app.RequestContext) (int64, error) {
	value, present, err := singleHeader(request, "If-Match")
	if err != nil || !present {
		return 0, errors.New("If-Match must occur once")
	}
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, errors.New("If-Match must be a strong numeric ETag")
	}
	decimal := value[1 : len(value)-1]
	if !positiveDecimalPattern.MatchString(decimal) {
		return 0, errors.New("If-Match must be a strong numeric ETag")
	}
	revision, parseErr := strconv.ParseInt(decimal, 10, 64)
	if parseErr != nil || formatETag(revision) != value {
		return 0, errors.New("If-Match must be a strong numeric ETag")
	}
	return revision, nil
}

func formatETag(revision int64) string {
	return `"` + strconv.FormatInt(revision, 10) + `"`
}

func pathUUID(request *app.RequestContext, name string) (string, error) {
	if request == nil {
		return "", errors.New("request is required")
	}
	value := request.Param(name)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.Version() != 7 || parsed.String() != value {
		return "", fmt.Errorf("%s must be a canonical UUIDv7", name)
	}
	return value, nil
}

func pathSlug(request *app.RequestContext) (string, error) {
	if request == nil {
		return "", errors.New("request is required")
	}
	value := request.Param("slug")
	if len(value) < 3 || len(value) > 80 || !slugPattern.MatchString(value) {
		return "", errors.New("slug is invalid")
	}
	return value, nil
}

func pathUserID(request *app.RequestContext) (int64, error) {
	if request == nil {
		return 0, errors.New("request is required")
	}
	value := request.Param("user_id")
	if !positiveDecimalPattern.MatchString(value) {
		return 0, errors.New("user_id must be a positive decimal integer")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("user_id must be a positive decimal integer")
	}
	return parsed, nil
}

func upstreamContext(ctx context.Context, request *app.RequestContext) context.Context {
	if token, ok := gatewaymiddleware.AccessToken(request); ok {
		return coreauth.WithAccessToken(ctx, token)
	}
	return ctx
}

func endpointURL(options config.EndpointOptions, path string) string {
	return strings.TrimRight(options.PublicBaseURL, "/") + path
}

func containsControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

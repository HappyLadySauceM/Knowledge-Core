package domain

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const SchemaVersion int32 = 1

var (
	ErrInvalid      = errors.New("configuration is invalid")
	ErrNotFound     = errors.New("configuration not found")
	ErrConflict     = errors.New("configuration conflict")
	ErrPrecondition = errors.New("configuration precondition failed")
)

type ConsumerState struct {
	Environment     string
	Namespace       string
	Consumer        string
	DesiredRevision int64
	AppliedRevision int64
	Status          string
	LastErrorKey    string
}

type DeliveryUpdate struct {
	MessageID    string
	Namespace    string
	Revision     int64
	Consumer     string
	Status       string
	Attempts     int
	LastErrorKey string
}

type Snapshot struct {
	Environment   string
	Namespace     string
	Revision      int64
	SchemaVersion int32
	Public        map[string]string
	Secrets       map[string]string
	UpdatedBy     int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type OutboxMessage struct {
	ID        string
	Subject   string
	Payload   []byte
	Headers   map[string]string
	Attempts  int
	Namespace string
	Revision  int64
}

func NewID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7: %w", err)
	}
	return id.String(), nil
}

func Default(namespace string) (Snapshot, error) {
	switch namespace {
	case "site":
		return Snapshot{Namespace: namespace, SchemaVersion: SchemaVersion, Public: map[string]string{
			"title": "HappyLadySauce", "tagline_zh": "把值得留下的想法，写成值得阅读的文章。",
			"tagline_en": "Turn ideas worth keeping into pages worth reading.", "hero_image_url": "/images/home-hero.png",
			"hero_focal_x": "50", "hero_focal_y": "50", "hero_attachment_id": "",
		}, Secrets: map[string]string{}}, nil
	case "email":
		return Snapshot{Namespace: namespace, SchemaVersion: SchemaVersion, Public: map[string]string{
			"enabled": "false", "host": "", "port": "587", "username": "", "from": "", "frontend_base_url": "",
		}, Secrets: map[string]string{}}, nil
	case "ai":
		return Snapshot{Namespace: namespace, SchemaVersion: SchemaVersion, Public: map[string]string{
			"enabled": "false", "provider": "openai-compatible", "base_url": "", "model": "", "request_timeout_ms": "30000", "max_tokens": "4096",
		}, Secrets: map[string]string{}}, nil
	default:
		return Snapshot{}, fmt.Errorf("%w: unsupported namespace", ErrInvalid)
	}
}

func Validate(namespace string, values, existingSecrets map[string]string) (map[string]string, map[string]string, error) {
	defaults, err := Default(namespace)
	if err != nil {
		return nil, nil, err
	}
	public := clone(defaults.Public)
	secrets := clone(existingSecrets)
	allowedSecrets := secretKeys(namespace)
	for key, value := range values {
		if _, ok := defaults.Public[key]; ok {
			public[key] = strings.TrimSpace(value)
			continue
		}
		if _, ok := allowedSecrets[key]; ok {
			value = strings.TrimSpace(value)
			if value == "" {
				delete(secrets, key)
			} else {
				secrets[key] = value
			}
			continue
		}
		return nil, nil, fmt.Errorf("%w: unknown %s key %q", ErrInvalid, namespace, key)
	}
	if err := validateNamespace(namespace, public, secrets); err != nil {
		return nil, nil, err
	}
	return public, secrets, nil
}

func SecretKeys(namespace string) []string {
	keys := make([]string, 0, len(secretKeys(namespace)))
	for key := range secretKeys(namespace) {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func AllKeys(snapshot Snapshot) []string {
	keys := make([]string, 0, len(snapshot.Public)+len(snapshot.Secrets))
	for key := range snapshot.Public {
		keys = append(keys, key)
	}
	for key := range snapshot.Secrets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func secretKeys(namespace string) map[string]struct{} {
	switch namespace {
	case "email":
		return map[string]struct{}{"password": {}}
	case "ai":
		return map[string]struct{}{"api_key": {}}
	default:
		return map[string]struct{}{}
	}
}

func validateNamespace(namespace string, public, secrets map[string]string) error {
	switch namespace {
	case "site":
		if length(public["title"], 1, 120) != nil || length(public["tagline_zh"], 1, 300) != nil || length(public["tagline_en"], 1, 300) != nil || length(public["hero_image_url"], 1, 2048) != nil {
			return fmt.Errorf("%w: site text fields are invalid", ErrInvalid)
		}
		if attachmentID := public["hero_attachment_id"]; attachmentID != "" {
			if _, err := uuid.Parse(attachmentID); err != nil {
				return fmt.Errorf("%w: hero_attachment_id must be a UUID", ErrInvalid)
			}
		}
		for _, key := range []string{"hero_focal_x", "hero_focal_y"} {
			value, err := strconv.ParseFloat(public[key], 64)
			if err != nil || value < 0 || value > 100 {
				return fmt.Errorf("%w: %s must be between 0 and 100", ErrInvalid, key)
			}
		}
	case "email":
		enabled, err := strconv.ParseBool(public["enabled"])
		if err != nil {
			return fmt.Errorf("%w: email.enabled must be boolean", ErrInvalid)
		}
		port, portErr := strconv.Atoi(public["port"])
		if portErr != nil || (port != 465 && port != 587) {
			return fmt.Errorf("%w: email.port must be 465 or 587", ErrInvalid)
		}
		if enabled {
			if length(public["host"], 1, 253) != nil || length(public["username"], 1, 320) != nil || length(secrets["password"], 1, 4096) != nil {
				return fmt.Errorf("%w: enabled email configuration requires host, username, and password", ErrInvalid)
			}
			if _, err := mail.ParseAddress(public["from"]); err != nil {
				return fmt.Errorf("%w: email.from is invalid", ErrInvalid)
			}
			if !validHTTPOrigin(public["frontend_base_url"]) {
				return fmt.Errorf("%w: email.frontend_base_url must be an HTTP origin", ErrInvalid)
			}
		}
	case "ai":
		enabled, err := strconv.ParseBool(public["enabled"])
		if err != nil {
			return fmt.Errorf("%w: ai.enabled must be boolean", ErrInvalid)
		}
		if public["provider"] != "openai-compatible" && public["provider"] != "deepseek" {
			return fmt.Errorf("%w: ai.provider is unsupported", ErrInvalid)
		}
		timeout, timeoutErr := strconv.Atoi(public["request_timeout_ms"])
		maxTokens, tokenErr := strconv.Atoi(public["max_tokens"])
		if timeoutErr != nil || timeout < 1000 || timeout > 300000 || tokenErr != nil || maxTokens < 1 || maxTokens > 1048576 {
			return fmt.Errorf("%w: AI limits are invalid", ErrInvalid)
		}
		if enabled && (!validHTTPSURL(public["base_url"]) || length(public["model"], 1, 200) != nil || length(secrets["api_key"], 1, 8192) != nil) {
			return fmt.Errorf("%w: enabled AI configuration requires HTTPS URL, model, and API key", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported namespace", ErrInvalid)
	}
	return nil
}

func length(value string, minimum, maximum int) error {
	length := len([]rune(strings.TrimSpace(value)))
	if length < minimum || length > maximum {
		return ErrInvalid
	}
	return nil
}

func validHTTPOrigin(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed != nil && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && (parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validHTTPSURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed != nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.Fragment == ""
}

func clone(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

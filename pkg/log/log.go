// Package log builds structured slog loggers with request and trace metadata.
package log

import (
	"context"
	"encoding"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"unicode"

	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"go.opentelemetry.io/otel/trace"
)

const redactedValue = "[REDACTED]"

type Options struct {
	Service     string
	Environment string
	Level       string
	AddSource   bool
	Output      io.Writer
}

// New creates a JSON logger and its dynamic level control. Callers may retain
// the LevelVar and change it at runtime without rebuilding the logger.
func New(service, environment, level string, output io.Writer) (*slog.Logger, *slog.LevelVar, error) {
	return NewWithOptions(Options{
		Service:     service,
		Environment: environment,
		Level:       level,
		Output:      output,
	})
}

// NewWithOptions creates a JSON logger while preserving the same context
// enrichment and redaction contract as New.
func NewWithOptions(options Options) (*slog.Logger, *slog.LevelVar, error) {
	if strings.TrimSpace(options.Service) == "" {
		return nil, nil, errors.New("create logger: service is required")
	}
	if strings.TrimSpace(options.Environment) == "" {
		return nil, nil, errors.New("create logger: environment is required")
	}
	parsedLevel, err := ParseLevel(options.Level)
	if err != nil {
		return nil, nil, err
	}
	output := outputOrStderr(options.Output)
	levelVar := new(slog.LevelVar)
	levelVar.Set(parsedLevel)
	handlerOptions := &slog.HandlerOptions{Level: levelVar, AddSource: options.AddSource}
	base := slog.NewJSONHandler(output, handlerOptions)
	handler := &contextHandler{Handler: base, boundKeys: make(map[string]struct{})}
	logger := slog.New(handler).With(
		slog.String("service", strings.TrimSpace(options.Service)),
		slog.String("environment", strings.TrimSpace(options.Environment)),
	)
	return logger, levelVar, nil
}

func NewBootstrap(service string, output io.Writer) *slog.Logger {
	if strings.TrimSpace(service) == "" {
		service = "unknown"
	}
	logger, _, err := New(service, "bootstrap", "info", output)
	if err != nil {
		// All inputs above are known-valid; keep bootstrap logging available if a
		// future validation rule changes.
		return slog.New(slog.NewJSONHandler(outputOrStderr(output), nil))
	}
	return logger
}

func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("create logger: unsupported level %q", value)
	}
}

func SetLevel(levelVar *slog.LevelVar, value string) error {
	if levelVar == nil {
		return errors.New("set log level: level control is nil")
	}
	level, err := ParseLevel(value)
	if err != nil {
		return err
	}
	levelVar.Set(level)
	return nil
}

func outputOrStderr(output io.Writer) io.Writer {
	if output == nil {
		return os.Stderr
	}
	return output
}

type contextHandler struct {
	slog.Handler
	boundKeys map[string]struct{}
	inGroup   bool
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	seen := cloneKeys(h.boundKeys)
	record.Attrs(func(attr slog.Attr) bool {
		seen[attr.Key] = struct{}{}
		clean.AddAttrs(redactAttr(attr, 0))
		return true
	})
	for _, attr := range contextAttrs(ctx) {
		if _, exists := seen[attr.Key]; !exists {
			clean.AddAttrs(attr)
		}
	}
	return h.Handler.Handle(ctx, clean)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	keys := cloneKeys(h.boundKeys)
	for _, attr := range attrs {
		clean = append(clean, redactAttr(attr, 0))
		if !h.inGroup {
			keys[attr.Key] = struct{}{}
		}
	}
	return &contextHandler{Handler: h.Handler.WithAttrs(clean), boundKeys: keys, inGroup: h.inGroup}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name), boundKeys: cloneKeys(h.boundKeys), inGroup: true}
}

func contextAttrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	attrs := make([]slog.Attr, 0, 4)
	if requestID := metadata.RequestID(ctx); requestID != "" {
		attrs = append(attrs, slog.String("request_id", requestID))
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		attrs = append(attrs,
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	if userID := metadata.UserID(ctx); userID > 0 {
		attrs = append(attrs, slog.Int64("user_id", userID))
	}
	return attrs
}

func redactAttr(attr slog.Attr, depth int) slog.Attr {
	if attr.Equal(slog.Attr{}) {
		return attr
	}
	if SensitiveKey(attr.Key) {
		return slog.String(attr.Key, redactedValue)
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		group := value.Group()
		clean := make([]slog.Attr, 0, len(group))
		for _, child := range group {
			clean = append(clean, redactAttr(child, depth+1))
		}
		return slog.Group(attr.Key, attrsToAny(clean)...)
	}
	if value.Kind() == slog.KindAny {
		return slog.Any(attr.Key, redactAny(value.Any(), depth+1))
	}
	attr.Value = value
	return attr
}

func redactAny(value any, depth int) any {
	if value == nil {
		return value
	}
	if depth > 16 {
		return redactedValue
	}
	if _, ok := value.(error); ok {
		return value
	}
	if _, ok := value.(encoding.TextMarshaler); ok {
		return value
	}
	if _, ok := value.(stdjson.Marshaler); ok {
		return value
	}

	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return nil
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() {
		return nil
	}

	switch reflected.Kind() {
	case reflect.Map:
		if reflected.Type().Key().Kind() != reflect.String {
			return value
		}
		clean := make(map[string]any, reflected.Len())
		iterator := reflected.MapRange()
		for iterator.Next() {
			key := iterator.Key().String()
			if SensitiveKey(key) {
				clean[key] = redactedValue
				continue
			}
			clean[key] = redactAny(iterator.Value().Interface(), depth+1)
		}
		return clean
	case reflect.Array, reflect.Slice:
		if reflected.Kind() == reflect.Slice && reflected.IsNil() {
			return value
		}
		clean := make([]any, reflected.Len())
		for i := range reflected.Len() {
			clean[i] = redactAny(reflected.Index(i).Interface(), depth+1)
		}
		return clean
	case reflect.Struct:
		clean := make(map[string]any, reflected.NumField())
		typeInfo := reflected.Type()
		for i := range reflected.NumField() {
			field := typeInfo.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := fieldName(field)
			if name == "" {
				continue
			}
			if SensitiveKey(name) {
				clean[name] = redactedValue
				continue
			}
			clean[name] = redactAny(reflected.Field(i).Interface(), depth+1)
		}
		return clean
	default:
		return value
	}
}

func fieldName(field reflect.StructField) string {
	for _, tagName := range []string{"json", "slog"} {
		if tag, exists := field.Tag.Lookup(tagName); exists {
			name := strings.Split(tag, ",")[0]
			if name == "-" {
				return ""
			}
			if name != "" {
				return name
			}
		}
	}
	return field.Name
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for i := range attrs {
		values[i] = attrs[i]
	}
	return values
}

func cloneKeys(source map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(source))
	for key := range source {
		clone[key] = struct{}{}
	}
	return clone
}

// SensitiveKey reports whether a field name should never be emitted as-is.
func SensitiveKey(key string) bool {
	key = strings.ToLower(splitCamelCase(strings.TrimSpace(key)))
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	for _, token := range strings.Split(normalized, "_") {
		switch token {
		case "password", "passwd", "pwd", "secret", "token", "authorization", "cookie", "dsn", "credential", "credentials":
			return true
		}
	}
	switch normalized {
	case "apikey", "api_key", "privatekey", "private_key", "request_body", "response_body", "payload":
		return true
	default:
		return false
	}
}

func splitCamelCase(value string) string {
	var result strings.Builder
	for i, current := range value {
		if i > 0 && unicode.IsUpper(current) {
			result.WriteByte('_')
		}
		result.WriteRune(current)
	}
	return result.String()
}

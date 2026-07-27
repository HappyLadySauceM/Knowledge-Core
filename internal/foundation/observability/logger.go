package observability

import (
	"io"
	"log/slog"
	"strings"
)

func NewJSONLogger(output io.Writer, level string, service string) *slog.Logger {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		parsed = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: parsed})
	return slog.New(handler).With("service", service)
}

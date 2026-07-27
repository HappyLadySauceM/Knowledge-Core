package observability_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/observability"
)

func TestNewJSONLoggerIncludesService(t *testing.T) {
	var output bytes.Buffer
	logger := observability.NewJSONLogger(&output, "info", "gateway")
	logger.InfoContext(context.Background(), "started", "address", ":8080")

	line := output.String()
	for _, expected := range []string{`"service":"gateway"`, `"msg":"started"`, `"address":":8080"`} {
		if !strings.Contains(line, expected) {
			t.Fatalf("log output %q does not contain %q", line, expected)
		}
	}
}

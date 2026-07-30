package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/apperror"
	"github.com/HappyLadySauce/Knowledge-Core/internal/rpcerror"
	"go.opentelemetry.io/otel/trace"
)

func TestFinishRPCLogsApplicationMetadataAndRestrictsCauses(t *testing.T) {
	tests := []struct {
		name       string
		definition apperror.Definition
		code       int32
		wantLevel  string
		wantCause  bool
	}{
		{
			name: "internal cause is recorded",
			definition: apperror.MustDefine(
				"knowledge.internal", apperror.KindInternal, "internal service error",
			),
			code: 30999, wantLevel: "ERROR", wantCause: true,
		},
		{
			name: "invalid input cause stays out of logs",
			definition: apperror.MustDefine(
				"knowledge.invalid_input", apperror.KindInvalidArgument, "invalid request",
			),
			code: 30001, wantLevel: "INFO", wantCause: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			cause := errors.New("private database detail")
			finishRPC(
				logger,
				context.Background(),
				trace.SpanFromContext(context.Background()),
				"server",
				"knowledge-core.knowledge",
				"GetDocument",
				time.Now(),
				rpcerror.New(test.code, test.definition, cause),
			)

			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatalf("decode log %q: %v", output.String(), err)
			}
			if record["level"] != test.wantLevel ||
				record["error_code"] != strconv.FormatInt(int64(test.code), 10) ||
				record["error_key"] != test.definition.Key() ||
				record["error_kind"] != string(test.definition.Kind()) {
				t.Fatalf("application error log = %#v", record)
			}
			_, hasCause := record["error"]
			if hasCause != test.wantCause {
				t.Fatalf("application error cause present = %t, want %t; log = %#v", hasCause, test.wantCause, record)
			}
		})
	}
}

package log_test

import (
	"bytes"
	"context"
	"testing"

	logpkg "github.com/HappyLadySauce/Knowledge-Core/pkg/log"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/metadata"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/cloudwego/kitex/pkg/klog"
)

func TestInstallCloudWeGoPreservesContext(t *testing.T) {
	previousHertz := hlog.DefaultLogger()
	previousKitex := klog.DefaultLogger()
	t.Cleanup(func() {
		hlog.SetLogger(previousHertz)
		klog.SetLogger(previousKitex)
	})

	var output bytes.Buffer
	logger, level, err := logpkg.New("identity", "test", "info", &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logpkg.InstallCloudWeGo(logger, level)
	ctx := metadata.WithRequestID(context.Background(), "request-1")
	hlog.CtxInfof(ctx, "request %s", "complete")
	klog.Warn("registry unavailable")

	if !bytes.Contains(output.Bytes(), []byte(`"component":"hertz"`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"request_id":"request-1"`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"component":"kitex"`)) {
		t.Fatalf("CloudWeGo logs were not structured/enriched: %s", output.String())
	}
}

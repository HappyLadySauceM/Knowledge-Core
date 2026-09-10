package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func TestGenerateCoversGatewayAndCollaborationContracts(t *testing.T) {
	root := repositoryRoot(t)
	value, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	paths, ok := value.OpenAPI["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatalf("OpenAPI paths = %#v", value.OpenAPI["paths"])
	}
	operationCount := 0
	for _, raw := range paths {
		pathItem, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("path item = %#v", raw)
		}
		for method := range pathItem {
			switch method {
			case "get", "post", "put", "patch", "delete":
				operationCount++
			}
		}
	}
	if operationCount != 50 {
		t.Fatalf("OpenAPI operation count = %d, want 50", operationCount)
	}
	if got := value.AsyncAPI["asyncapi"]; got != "3.0.0" {
		t.Fatalf("AsyncAPI version = %#v", got)
	}
	if _, ok := value.AsyncAPI["x-close-codes"]; !ok {
		t.Fatal("AsyncAPI close-code metadata is missing")
	}
}

func TestGeneratedFilesAreCurrent(t *testing.T) {
	root := repositoryRoot(t)
	value, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeOrCheck(root, value, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"openapi.yaml", "openapi.json", "asyncapi.yaml", "asyncapi.json", "index.html", "http.html", "websocket.html", "style.css"} {
		if _, err := os.Stat(filepath.Join(root, "api", name)); err != nil {
			t.Fatalf("generated asset %s: %v", name, err)
		}
	}
}

func TestGeneratedJSONRemainsValid(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"openapi.json", "asyncapi.json"} {
		contents, err := os.ReadFile(filepath.Join(root, "api", name))
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(contents, &value); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

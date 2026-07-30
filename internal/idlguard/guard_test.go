package idlguard_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HappyLadySauce/Knowledge-Core/internal/idlguard"
)

func TestServiceFilesUsesParsedServiceDefinitions(t *testing.T) {
	root := t.TempDir()
	writeIDL(t, root, "common.thrift", "namespace go common\nstruct Item { 1: optional string value }\n")
	writeIDL(t, root, "service.thrift", "namespace go sample\nservice Sample { void Ping() }\n")

	files, err := idlguard.ServiceFiles(root)
	if err != nil {
		t.Fatalf("ServiceFiles() error = %v", err)
	}
	if len(files) != 1 || files[0] != "service.thrift" {
		t.Fatalf("ServiceFiles() = %#v", files)
	}
}

func TestCompareTreesAllowsOptionalAdditions(t *testing.T) {
	base, current := t.TempDir(), t.TempDir()
	writeIDL(t, base, "api.thrift", `namespace go sample
struct Response { 1: required string value }
service Sample { Response Get(1: required string id) }
`)
	writeIDL(t, current, "api.thrift", `namespace go sample
struct Response { 1: required string value, 2: optional string trace_id }
service Sample { Response Get(1: required string id, 2: optional string locale) }
`)
	if err := idlguard.CompareTrees(base, current); err != nil {
		t.Fatalf("CompareTrees() error = %v", err)
	}
}

func TestCompareTreesRejectsBreakingChanges(t *testing.T) {
	base, current := t.TempDir(), t.TempDir()
	writeIDL(t, base, "api.thrift", `namespace go sample
struct Response { 1: required string value }
service Sample { Response Get(1: required string id) }
`)
	writeIDL(t, current, "api.thrift", `namespace go sample
struct Response { 1: required i64 value, 2: required string trace_id }
service Sample { string Get(1: required string renamed) }
`)
	err := idlguard.CompareTrees(base, current)
	if err == nil {
		t.Fatal("CompareTrees() accepted breaking changes")
	}
	for _, expected := range []string{"changed type", "added required", "changed its return", "renamed"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("CompareTrees() error %q does not contain %q", err, expected)
		}
	}
}

func TestCompareTreesRejectsHTTPAnnotationChanges(t *testing.T) {
	base, current := t.TempDir(), t.TempDir()
	writeIDL(t, base, "api.thrift", `namespace go sample
struct Request { 1: required string id (api.path="id") }
service Sample { string Get(1: required Request request) (api.get="/items/:id") }
`)
	writeIDL(t, current, "api.thrift", `namespace go sample
struct Request { 1: required string id (api.query="id") }
service Sample { string Get(1: required Request request) (api.get="/renamed/:id") }
`)
	err := idlguard.CompareTrees(base, current)
	if err == nil {
		t.Fatal("CompareTrees() accepted HTTP annotation changes")
	}
	if occurrences := strings.Count(err.Error(), "changed API annotations"); occurrences != 2 {
		t.Fatalf("CompareTrees() error = %q, want two API annotation violations", err)
	}
}

func writeIDL(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

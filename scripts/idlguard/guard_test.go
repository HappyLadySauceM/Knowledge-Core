package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceFilesUsesParsedDefinitions(t *testing.T) {
	root := t.TempDir()
	writeIDL(t, root, "common.thrift", "namespace go common\nstruct Item { 1: optional string value }\n")
	writeIDL(t, root, "nested/service.thrift", "namespace go sample\nservice Sample { void Ping() }\n")

	files, err := serviceFiles(root)
	if err != nil {
		t.Fatalf("serviceFiles() error = %v", err)
	}
	want := filepath.ToSlash(filepath.Join("nested", "service.thrift"))
	if len(files) != 1 || files[0] != want {
		t.Fatalf("serviceFiles() = %#v, want [%q]", files, want)
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
	if err := compareTrees(base, current); err != nil {
		t.Fatalf("compareTrees() error = %v", err)
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
	err := compareTrees(base, current)
	if err == nil {
		t.Fatal("compareTrees() accepted breaking changes")
	}
	for _, expected := range []string{"changed type", "added required", "changed its return", "renamed"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("compareTrees() error %q does not contain %q", err, expected)
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
	err := compareTrees(base, current)
	if err == nil {
		t.Fatal("compareTrees() accepted HTTP annotation changes")
	}
	if occurrences := strings.Count(err.Error(), "changed API annotations"); occurrences != 2 {
		t.Fatalf("compareTrees() error = %q, want two API annotation violations", err)
	}
}

func TestCompareTreesAllowsNewConstantAndRejectsChangedConstant(t *testing.T) {
	base, compatible, incompatible := t.TempDir(), t.TempDir(), t.TempDir()
	writeIDL(t, base, "api.thrift", "namespace go sample\nconst i32 StableCode = 100\n")
	writeIDL(t, compatible, "api.thrift", "namespace go sample\nconst i32 StableCode = 100\nconst i32 NewCode = 101\n")
	writeIDL(t, incompatible, "api.thrift", "namespace go sample\nconst i32 StableCode = 999\n")

	if err := compareTrees(base, compatible); err != nil {
		t.Fatalf("compareTrees() rejected an additive constant: %v", err)
	}
	if err := compareTrees(base, incompatible); err == nil || !strings.Contains(err.Error(), "constant StableCode was removed or changed") {
		t.Fatalf("compareTrees() changed constant error = %v", err)
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

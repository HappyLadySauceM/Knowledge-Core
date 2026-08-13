package configcenter

import (
	"testing"
)

func TestManagerAppliesOnlyMonotonicUnambiguousRevisions(t *testing.T) {
	t.Parallel()
	manager := &Manager{}
	applied := 0
	manager.apply = func(DynamicDocument) (ApplyResult, error) {
		applied++
		return ApplyResult{}, nil
	}
	initial := DynamicDocument{
		APIVersion: DynamicAPIVersion,
		Kind:       DynamicKind,
		Revision:   3,
		Log:        DynamicLog{Level: "info"},
	}
	if err := manager.applyDocument(initial); err != nil {
		t.Fatalf("apply initial document: %v", err)
	}
	if err := manager.applyDocument(initial); err != nil {
		t.Fatalf("replay initial document: %v", err)
	}
	conflict := initial
	conflict.Log.Level = "warn"
	if err := manager.applyDocument(conflict); err == nil {
		t.Fatal("same revision with different contents must fail")
	}
	rollback := initial
	rollback.Revision = 2
	if err := manager.applyDocument(rollback); err == nil {
		t.Fatal("revision rollback must fail")
	}
	next := initial
	next.Revision = 4
	next.Log.Level = "debug"
	if err := manager.applyDocument(next); err != nil {
		t.Fatalf("apply next document: %v", err)
	}
	if applied != 2 {
		t.Fatalf("apply count: got %d, want 2", applied)
	}
}

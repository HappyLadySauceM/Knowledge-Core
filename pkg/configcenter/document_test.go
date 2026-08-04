package configcenter

import "testing"

func TestDecodeDynamicDocumentIsStrict(t *testing.T) {
	t.Parallel()
	valid := []byte("api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 7\nlog:\n  level: warn\n")
	document, err := DecodeDynamicDocument(valid)
	if err != nil {
		t.Fatalf("decode valid document: %v", err)
	}
	if document.Revision != 7 || document.Log.Level != "warn" {
		t.Fatalf("unexpected document: %#v", document)
	}
	for _, invalid := range [][]byte{
		[]byte("api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 0\nlog:\n  level: info\n"),
		[]byte("api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 1\nlog:\n  level: verbose\n"),
		[]byte("api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 1\nlog:\n  level: info\nextra: true\n"),
		[]byte("api_version: knowledge-core.io/v1alpha1\nkind: DynamicConfig\nrevision: 1\nlog:\n  level: info\n---\nrevision: 2\n"),
	} {
		if _, err := DecodeDynamicDocument(invalid); err == nil {
			t.Fatalf("invalid document accepted: %s", invalid)
		}
	}
}

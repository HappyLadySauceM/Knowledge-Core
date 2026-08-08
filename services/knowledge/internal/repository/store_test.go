package repository

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildListDocumentsQueryUsesSearchCandidates(t *testing.T) {
	options := ListOptions{ActorID: 42, Query: ` 100%_\ `}
	query, args := buildListDocumentsQuery(options, 20)

	for _, fragment := range []string{
		"JOIN (",
		"SELECT id AS document_id",
		"UNION",
		"WHERE plain_text ILIKE ?",
		") matched ON matched.document_id = d.id",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("query missing %q:\n%s", fragment, query)
		}
	}
	if strings.Contains(query, "COALESCE(p.plain_text") {
		t.Fatalf("query still searches across the joined projection row:\n%s", query)
	}
	pattern := "%" + escapeLike(options.Query) + "%"
	want := []any{int64(42), int64(42), pattern, pattern, pattern, int64(42), 21}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v, want %#v", args, want)
	}
}

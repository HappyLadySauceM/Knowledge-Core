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

func TestBuildListDocumentsQueryUsesPublicationSnapshotAndDeletionGuards(t *testing.T) {
	query, _ := buildListDocumentsQuery(ListOptions{ActorID: 42, Published: true}, 20)
	for _, fragment := range []string{
		"LEFT JOIN knowledge.document_publications pub ON pub.document_id = d.id",
		"pub.document_id IS NOT NULL",
		"d.publication_status = 'published'",
		"d.deleted_at IS NULL",
		"pub.publication_hash AS publication_hash",
		"pub.cover_attachment_id AS publication_cover_attachment_id",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("public query missing %q:\n%s", fragment, query)
		}
	}
	if strings.Contains(query, "d.published =") {
		t.Fatalf("public query still trusts the removed published flag:\n%s", query)
	}
}

func TestBuildListDocumentsQueryHidesPurgePendingTrash(t *testing.T) {
	query, _ := buildListDocumentsQuery(ListOptions{ActorID: 42, Deleted: true}, 20)
	if !strings.Contains(query, "d.purge_after IS NULL OR d.purge_after > CURRENT_TIMESTAMP") {
		t.Fatalf("trash query does not hide permanently deleted entries:\n%s", query)
	}
}

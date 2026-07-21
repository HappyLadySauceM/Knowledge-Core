package document

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/taxonomy"
	"github.com/HappyLadySauce/Knowledge-Core/internal/testutil"
	"github.com/HappyLadySauce/Knowledge-Core/internal/user"
)

func TestDocumentServiceWritesMarkdownAndIndexesPublishedDocuments(t *testing.T) {
	ctx := context.Background()
	db := newDocumentTestDB(t)
	taxonomies := taxonomy.NewService(db)
	category, err := taxonomies.CreateCategory(ctx, taxonomy.CategoryCommand{Name: "Tech", Slug: "tech"})
	if err != nil {
		t.Fatalf("create category failed: %v", err)
	}
	tag, err := taxonomies.CreateTag(ctx, taxonomy.TagCommand{Name: "Go", Slug: "go"})
	if err != nil {
		t.Fatalf("create tag failed: %v", err)
	}
	service, err := NewService(db)
	if err != nil {
		t.Fatalf("create document service failed: %v", err)
	}
	admin := user.User{ID: 1, Role: user.RoleAdmin}

	created, err := service.CreateAdmin(ctx, admin, CreateCommand{
		Title:      "Go Concurrency",
		Summary:    "Goroutine and channel notes",
		Content:    "Goroutine and channel examples",
		CategoryID: category.ID,
		TagIDs:     []int64{tag.ID},
		Status:     StatusPublished,
	})
	if err != nil {
		t.Fatalf("create document failed: %v", err)
	}
	if created.Status != StatusPublished || created.PublishedAt == nil {
		t.Fatalf("unexpected published document: %+v", created.Document)
	}
	if created.Content != "Goroutine and channel examples" || len(created.Blocks) != 1 {
		t.Fatalf("unexpected created blocks/content: %+v content=%q", created.Blocks, created.Content)
	}
	publicDetail, err := service.GetPublic(ctx, created.ID)
	if err != nil {
		t.Fatalf("get public published document failed: %v", err)
	}
	if publicDetail.Content != "Goroutine and channel examples" || len(publicDetail.Blocks) != 1 {
		t.Fatalf("unexpected public revision: %+v content=%q", publicDetail.Blocks, publicDetail.Content)
	}
	publicBySlug, err := service.GetPublicBySlug(ctx, created.Slug)
	if err != nil {
		t.Fatalf("get public document by slug failed: %v", err)
	}
	if publicBySlug.ID != created.ID || publicBySlug.Content != created.Content {
		t.Fatalf("public document by slug = %+v, want id=%d content=%q", publicBySlug, created.ID, created.Content)
	}

	list, err := service.ListPublic(ctx, ListQuery{Q: "goroutine"})
	if err != nil {
		t.Fatalf("list public failed: %v", err)
	}
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("public list = %+v, want one item", list)
	}
	second, err := service.CreateAdmin(ctx, admin, CreateCommand{Title: "Newer Publication", Content: "new body", Status: StatusPublished})
	if err != nil {
		t.Fatalf("create second document failed: %v", err)
	}
	ordered, err := service.ListPublic(ctx, ListQuery{})
	if err != nil {
		t.Fatalf("list ordered public documents failed: %v", err)
	}
	if len(ordered.Items) != 2 || ordered.Items[0].ID != second.ID || ordered.Items[1].ID != created.ID {
		t.Fatalf("public order = %+v, want newer publication first", ordered.Items)
	}
	staleVersion := created.CurrentVersion - 1
	if _, err := service.UpdateAdmin(ctx, admin, created.ID, UpdateCommand{ExpectedVersion: &staleVersion}); !errors.Is(err, apperrors.Conflict) {
		t.Fatalf("stale update error = %v, want conflict", err)
	}

	draftStatus := StatusDraft
	updated, err := service.UpdateAdmin(ctx, admin, created.ID, UpdateCommand{Status: &draftStatus})
	if err != nil {
		t.Fatalf("unpublish document failed: %v", err)
	}
	if updated.Status != StatusDraft || updated.PublishedAt != nil {
		t.Fatalf("unexpected draft document: %+v", updated.Document)
	}
	if _, err := service.GetPublic(ctx, created.ID); !errors.Is(err, apperrors.NotFound) {
		t.Fatalf("public get draft error = %v, want not found", err)
	}

	if err := service.DeleteAdmin(ctx, admin, created.ID); err != nil {
		t.Fatalf("delete document failed: %v", err)
	}
	if err := service.DeleteAdmin(ctx, admin, second.ID); err != nil {
		t.Fatalf("delete second document failed: %v", err)
	}
	if _, err := service.GetAdmin(ctx, admin, created.ID); !errors.Is(err, apperrors.NotFound) {
		t.Fatalf("get deleted document error = %v, want not found", err)
	}
}

func TestDocumentServiceApplyOpsIdempotencyAndConflict(t *testing.T) {
	ctx := context.Background()
	db := newDocumentTestDB(t)
	taxonomies := taxonomy.NewService(db)
	category, err := taxonomies.CreateCategory(ctx, taxonomy.CategoryCommand{Name: "Tech", Slug: "tech"})
	if err != nil {
		t.Fatalf("create category failed: %v", err)
	}
	service, err := NewService(db)
	if err != nil {
		t.Fatalf("create document service failed: %v", err)
	}
	admin := user.User{ID: 1, Role: user.RoleAdmin}
	cmd := CreateCommand{
		Slug:       "ops-doc",
		Title:      "Ops Doc",
		Content:    "body",
		CategoryID: category.ID,
	}
	created, err := service.CreateAdmin(ctx, admin, cmd)
	if err != nil {
		t.Fatalf("create document failed: %v", err)
	}
	if len(created.Blocks) != 1 || created.Blocks[0].TextContent != "body" {
		t.Fatalf("created blocks = %+v, want one body block", created.Blocks)
	}
	payload := `{"text_content":"updated body"}`
	op := Operation{
		OpID:                 "op-1",
		BaseDocumentVersion:  created.CurrentVersion,
		BlockID:              created.Blocks[0].BlockID,
		ExpectedBlockVersion: created.Blocks[0].Version,
		Type:                 OpTypeUpdateBlock,
		PayloadJSON:          payload,
	}
	first, err := service.ApplyOpsAdmin(ctx, admin, created.ID, ApplyOpsCommand{Ops: []Operation{op}})
	if err != nil {
		t.Fatalf("apply op failed: %v", err)
	}
	if len(first.Acks) != 1 || first.Blocks[0].TextContent != "updated body" {
		t.Fatalf("first apply = %+v blocks=%+v, want ack and updated body", first.Acks, first.Blocks)
	}
	second, err := service.ApplyOpsAdmin(ctx, admin, created.ID, ApplyOpsCommand{Ops: []Operation{op}})
	if err != nil {
		t.Fatalf("duplicate op failed: %v", err)
	}
	if len(second.Acks) != 1 || second.Document.CurrentVersion != first.Document.CurrentVersion {
		t.Fatalf("duplicate result = %+v, want same version %d", second, first.Document.CurrentVersion)
	}
	stale := Operation{
		OpID:                 "op-2",
		BaseDocumentVersion:  created.CurrentVersion,
		BlockID:              created.Blocks[0].BlockID,
		ExpectedBlockVersion: created.Blocks[0].Version,
		Type:                 OpTypeUpdateBlock,
		PayloadJSON:          `{"text_content":"stale"}`,
	}
	conflict, err := service.ApplyOpsAdmin(ctx, admin, created.ID, ApplyOpsCommand{Ops: []Operation{stale}})
	if !errors.Is(err, apperrors.Conflict) {
		t.Fatalf("stale op error = %v, want conflict", err)
	}
	if len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Block.TextContent != "updated body" {
		t.Fatalf("conflict = %+v, want current updated block", conflict.Conflicts)
	}
	list, err := service.ListAdmin(ctx, admin, ListQuery{PageSize: 10})
	if err != nil {
		t.Fatalf("list admin failed: %v", err)
	}
	if list.Total != 1 {
		t.Fatalf("document total = %d, want 1", list.Total)
	}
}

func TestDocumentServiceApplyOpsAcrossBlocksAndPublishedRevisionIsolation(t *testing.T) {
	ctx := context.Background()
	db := newDocumentTestDB(t)
	service, err := NewService(db)
	if err != nil {
		t.Fatalf("create document service failed: %v", err)
	}
	admin := user.User{ID: 1, Role: user.RoleAdmin}
	created, err := service.CreateAdmin(ctx, admin, CreateCommand{
		Slug:    "multi-block-doc",
		Title:   "Multi Block",
		Content: "first\n\nsecond",
		Status:  StatusPublished,
	})
	if err != nil {
		t.Fatalf("create document failed: %v", err)
	}
	if len(created.Blocks) != 2 {
		t.Fatalf("created blocks = %+v, want two blocks", created.Blocks)
	}
	publicBefore, err := service.GetPublic(ctx, created.ID)
	if err != nil {
		t.Fatalf("get public before edits failed: %v", err)
	}
	ops := []Operation{
		{
			OpID:                 "multi-op-1",
			BaseDocumentVersion:  created.CurrentVersion,
			BlockID:              created.Blocks[0].BlockID,
			ExpectedBlockVersion: created.Blocks[0].Version,
			Type:                 OpTypeUpdateBlock,
			PayloadJSON:          `{"text_content":"first updated"}`,
		},
		{
			OpID:                 "multi-op-2",
			BaseDocumentVersion:  created.CurrentVersion,
			BlockID:              created.Blocks[1].BlockID,
			ExpectedBlockVersion: created.Blocks[1].Version,
			Type:                 OpTypeUpdateBlock,
			PayloadJSON:          `{"text_content":"second updated"}`,
		},
	}
	applied, err := service.ApplyOpsAdmin(ctx, admin, created.ID, ApplyOpsCommand{Ops: ops})
	if err != nil {
		t.Fatalf("apply block ops failed: %v", err)
	}
	if len(applied.Acks) != 2 || applied.Document.CurrentVersion != created.CurrentVersion+2 {
		t.Fatalf("apply result = %+v, want two acks and version incremented twice", applied)
	}
	publicAfter, err := service.GetPublic(ctx, created.ID)
	if err != nil {
		t.Fatalf("get public after draft edits failed: %v", err)
	}
	if publicAfter.Content != publicBefore.Content {
		t.Fatalf("public revision changed after draft edits: before=%q after=%q", publicBefore.Content, publicAfter.Content)
	}
}

func TestDocumentCategoryFilterUsesPathOnly(t *testing.T) {
	ctx := context.Background()
	db := newDocumentTestDB(t)
	taxonomies := taxonomy.NewService(db)
	tech, err := taxonomies.CreateCategory(ctx, taxonomy.CategoryCommand{Name: "Tech", Slug: "tech"})
	if err != nil {
		t.Fatalf("create tech category failed: %v", err)
	}
	life, err := taxonomies.CreateCategory(ctx, taxonomy.CategoryCommand{Name: "Life", Slug: "life"})
	if err != nil {
		t.Fatalf("create life category failed: %v", err)
	}
	aiTech, err := taxonomies.CreateCategory(ctx, taxonomy.CategoryCommand{Name: "AI", Slug: "ai", ParentID: &tech.ID})
	if err != nil {
		t.Fatalf("create tech ai category failed: %v", err)
	}
	aiLife, err := taxonomies.CreateCategory(ctx, taxonomy.CategoryCommand{Name: "AI", Slug: "ai", ParentID: &life.ID})
	if err != nil {
		t.Fatalf("create life ai category failed: %v", err)
	}
	service, err := NewService(db)
	if err != nil {
		t.Fatalf("create document service failed: %v", err)
	}
	admin := user.User{ID: 1, Role: user.RoleAdmin}
	if _, err := service.CreateAdmin(ctx, admin, CreateCommand{
		Slug:       "tech-ai-doc",
		Title:      "Tech AI",
		Content:    "published body",
		CategoryID: aiTech.ID,
		Status:     StatusPublished,
	}); err != nil {
		t.Fatalf("create tech document failed: %v", err)
	}
	if _, err := service.CreateAdmin(ctx, admin, CreateCommand{
		Slug:       "life-ai-doc",
		Title:      "Life AI",
		Content:    "published body",
		CategoryID: aiLife.ID,
		Status:     StatusPublished,
	}); err != nil {
		t.Fatalf("create life document failed: %v", err)
	}

	ambiguous, err := service.ListPublic(ctx, ListQuery{Category: "ai"})
	if err != nil {
		t.Fatalf("list ambiguous category failed: %v", err)
	}
	if ambiguous.Total != 0 {
		t.Fatalf("ambiguous category total = %d, want 0", ambiguous.Total)
	}
	filtered, err := service.ListPublic(ctx, ListQuery{Category: "tech/ai"})
	if err != nil {
		t.Fatalf("list category path failed: %v", err)
	}
	if filtered.Total != 1 || filtered.Items[0].Slug != "tech-ai-doc" {
		t.Fatalf("filtered result = %+v, want tech-ai-doc", filtered)
	}
}

func newDocumentTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.NewDB(t)
	// Insert a test admin user so document author_id foreign keys are satisfied.
	// The migrations no longer auto-create an admin user.
	// 插入测试 admin 用户以满足文档 author_id 外键约束。
	// 迁移不再自动创建 admin 用户。
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO users (username, email, avatar, bio, password_hash, role, status, token_version, created_at, updated_at)
VALUES ('admin', '', '', '', '', 'admin', 'active', 0, $1, $2)`,
		time.Now().UTC(),
		time.Now().UTC()); err != nil {
		t.Fatalf("insert test admin failed: %v", err)
	}
	return db
}

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/codec/json"
	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	postgresadapter "github.com/HappyLadySauce/Knowledge-Core/internal/database/postgres"
	knowledgeapp "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
	migrationpostgres "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/migration/postgres"
	knowledgepostgres "github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository/postgres"
)

func TestDocumentLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("KC_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("KC_TEST_DATABASE_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migrationpostgres.Up(ctx, dsn); err != nil {
		t.Fatalf("migration Up() error = %v", err)
	}
	db, err := postgresadapter.NewProvider().Open(ctx, database.Config{DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 1})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = db.Close() }()
	service, err := knowledgeapp.NewService(db, knowledgepostgres.NewDocumentRepository(), json.New())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	suffix := time.Now().UTC().UnixNano()
	publishedToken := fmt.Sprintf("publishedtoken%d", suffix)
	draftToken := fmt.Sprintf("drafttoken%d", suffix)
	detail, err := service.Create(ctx, knowledgeapp.CreateInput{Title: publishedToken, Summary: "published summary", AuthorID: 42})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM knowledge.documents WHERE id = $1`, detail.Document.ID)
	}()
	operation := domain.Operation{
		DocumentID: detail.Document.ID, OperationID: fmt.Sprintf("integration-op-%d", detail.Document.ID), BaseDocumentVersion: detail.Document.CurrentVersion,
		BlockID: fmt.Sprintf("integration-block-%d", detail.Document.ID), BaseBlockVersion: 0, PositionKey: "a", ContentJSON: `{"text":"published"}`,
		TextContent: "published", ActorID: 42,
	}
	type operationResult struct {
		ack domain.OperationAck
		err error
	}
	start := make(chan struct{})
	results := make(chan operationResult, 2)
	for range 2 {
		go func() {
			<-start
			ack, applyErr := service.ApplyOperation(ctx, operation)
			results <- operationResult{ack: ack, err: applyErr}
		}()
	}
	close(start)
	first, second := <-results, <-results
	for index, result := range []operationResult{first, second} {
		if result.err != nil || result.ack.DocumentVersion != 1 || result.ack.BlockVersion != 1 {
			t.Fatalf("concurrent ApplyOperation() result %d = %#v, %v", index, result.ack, result.err)
		}
	}
	if first.ack.Duplicate == second.ack.Duplicate {
		t.Fatalf("concurrent duplicate flags = %v, %v", first.ack.Duplicate, second.ack.Duplicate)
	}

	mismatched := operation
	mismatched.TextContent = "different replay"
	if _, err := service.ApplyOperation(ctx, mismatched); !errors.Is(err, knowledgeapp.ErrVersionConflict) {
		t.Fatalf("mismatched ApplyOperation() error = %v", err)
	}
	if _, err := service.SetStatus(ctx, detail.Document.ID, domain.StatusPublished, 42); err != nil {
		t.Fatalf("SetStatus(published) error = %v", err)
	}
	published, err := service.GetPublished(ctx, detail.Document.ID)
	if err != nil || published.Document.Title != publishedToken || len(published.Blocks) != 1 || published.Blocks[0].TextContent != "published" {
		t.Fatalf("GetPublished() = %#v, %v", published, err)
	}
	if _, err := service.Update(ctx, knowledgeapp.UpdateInput{DocumentID: detail.Document.ID, Title: draftToken, Summary: "draft summary"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	published, err = service.GetPublished(ctx, detail.Document.ID)
	if err != nil || published.Document.Title != publishedToken || published.Document.Summary != "published summary" {
		t.Fatalf("published snapshot after draft update = %#v, %v", published, err)
	}
	oldSearch, err := service.ListPublished(ctx, knowledgeapp.ListInput{Query: publishedToken})
	if err != nil || oldSearch.Total != 1 || len(oldSearch.Items) != 1 || oldSearch.Items[0].ID != detail.Document.ID {
		t.Fatalf("ListPublished(old revision) = %#v, %v", oldSearch, err)
	}
	draftSearch, err := service.ListPublished(ctx, knowledgeapp.ListInput{Query: draftToken})
	if err != nil || draftSearch.Total != 0 || len(draftSearch.Items) != 0 {
		t.Fatalf("ListPublished(unpublished draft) = %#v, %v", draftSearch, err)
	}
	if _, err := service.SetStatus(ctx, detail.Document.ID, domain.StatusPublished, 42); err != nil {
		t.Fatalf("SetStatus(republish) error = %v", err)
	}
	draftSearch, err = service.ListPublished(ctx, knowledgeapp.ListInput{Query: draftToken})
	if err != nil || draftSearch.Total != 1 || len(draftSearch.Items) != 1 || draftSearch.Items[0].ID != detail.Document.ID {
		t.Fatalf("ListPublished(republished revision) = %#v, %v", draftSearch, err)
	}
}

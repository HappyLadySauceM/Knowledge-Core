package postgres_test

import (
	"context"
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
	detail, err := service.Create(ctx, knowledgeapp.CreateInput{Title: "First version", Summary: "published summary", AuthorID: 42})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	defer func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM knowledge.documents WHERE id = $1`, detail.Document.ID)
	}()
	ack, err := service.ApplyOperation(ctx, domain.Operation{
		DocumentID: detail.Document.ID, OperationID: "integration-op-1", BaseDocumentVersion: detail.Document.CurrentVersion,
		BlockID: "integration-block-1", BaseBlockVersion: 0, PositionKey: "a", ContentJSON: `{"text":"published"}`,
		TextContent: "published", ActorID: 42,
	})
	if err != nil || ack.DocumentVersion != 1 || ack.BlockVersion != 1 || ack.Duplicate {
		t.Fatalf("ApplyOperation() = %#v, %v", ack, err)
	}
	duplicate, err := service.ApplyOperation(ctx, domain.Operation{
		DocumentID: detail.Document.ID, OperationID: "integration-op-1", BaseDocumentVersion: 0,
		BlockID: "integration-block-1", BaseBlockVersion: 0, PositionKey: "a", ContentJSON: `{"text":"published"}`,
		TextContent: "published", ActorID: 42,
	})
	if err != nil || !duplicate.Duplicate || duplicate.DocumentVersion != ack.DocumentVersion {
		t.Fatalf("duplicate ApplyOperation() = %#v, %v", duplicate, err)
	}
	if _, err := service.SetStatus(ctx, detail.Document.ID, domain.StatusPublished, 42); err != nil {
		t.Fatalf("SetStatus(published) error = %v", err)
	}
	published, err := service.GetPublished(ctx, detail.Document.ID)
	if err != nil || published.Document.Title != "First version" || len(published.Blocks) != 1 || published.Blocks[0].TextContent != "published" {
		t.Fatalf("GetPublished() = %#v, %v", published, err)
	}
	if _, err := service.Update(ctx, knowledgeapp.UpdateInput{DocumentID: detail.Document.ID, Title: "Draft revision", Summary: "draft summary"}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	published, err = service.GetPublished(ctx, detail.Document.ID)
	if err != nil || published.Document.Title != "First version" || published.Document.Summary != "published summary" {
		t.Fatalf("published snapshot after draft update = %#v, %v", published, err)
	}
}

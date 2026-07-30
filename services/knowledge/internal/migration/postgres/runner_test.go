package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	knowledgemigrations "github.com/HappyLadySauce/Knowledge-Core/migrations/knowledge/postgres"
	"github.com/golang-migrate/migrate/v4"
	databasepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestUpRequiresDSN(t *testing.T) {
	err := Up(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "database DSN is required") {
		t.Fatalf("Up() error = %v", err)
	}
}

func TestDownMigrationPreservesMigrationLedgerSchema(t *testing.T) {
	contents, err := knowledgemigrations.Files.ReadFile("000001_create_documents.down.sql")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(strings.ToUpper(string(contents)), "DROP SCHEMA") {
		t.Fatal("base down migration must preserve the schema containing schema_migrations")
	}
}

func TestStrengtheningMigrationContainsReplayAndPublishedSearchState(t *testing.T) {
	contents, err := knowledgemigrations.Files.ReadFile("000002_strengthen_operations_and_publishing.up.sql")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, required := range []string{
		"base_block_version",
		"published_revision_id",
		"documents_published_revision_owner_fkey",
		"document_revisions_document_id_id_key",
		"documents_published_revision_idx",
		"search_vector",
		"USING GIN",
	} {
		if !strings.Contains(string(contents), required) {
			t.Fatalf("strengthening migration does not contain %q", required)
		}
	}
}

func TestMigrationsRoundTripIntegration(t *testing.T) {
	dsn := os.Getenv("KC_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("KC_TEST_DATABASE_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	targetDSN, dropDatabase := createTemporaryDatabase(t, ctx, dsn)
	defer dropDatabase()
	if err := Up(ctx, targetDSN); err != nil {
		t.Fatalf("initial Up() error = %v", err)
	}

	inspectionDB, err := sql.Open("pgx", targetDSN)
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	defer func() {
		if closeErr := inspectionDB.Close(); closeErr != nil {
			t.Errorf("close migrated database: %v", closeErr)
		}
	}()
	assertMigrationVersion(t, inspectionDB, 2)
	assertColumnExists(t, inspectionDB, "documents", "published_revision_id", true)

	migration, closeMigration := openEmbeddedMigration(t, targetDSN)
	defer func() {
		if closeErr := closeMigration(); closeErr != nil {
			t.Errorf("close migration runner: %v", closeErr)
		}
	}()
	if err := migration.Steps(-1); err != nil {
		t.Fatalf("down migration 2 error = %v", err)
	}
	version, dirty, err := migration.Version()
	if err != nil || version != 1 || dirty {
		t.Fatalf("version after down migration 2 = %d, dirty %t, err %v", version, dirty, err)
	}
	assertColumnExists(t, inspectionDB, "documents", "published_revision_id", false)
	assertRelationExists(t, inspectionDB, "knowledge.document_revisions", true)

	if err := migration.Steps(-1); err != nil {
		t.Fatalf("down migration 1 error = %v", err)
	}
	if _, _, err := migration.Version(); !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("version after full rollback error = %v, want ErrNilVersion", err)
	}
	assertRelationExists(t, inspectionDB, "knowledge.documents", false)
	assertRelationExists(t, inspectionDB, "knowledge.schema_migrations", true)

	if err := closeMigration(); err != nil {
		t.Fatalf("close migration runner before reapply: %v", err)
	}
	if err := Up(ctx, targetDSN); err != nil {
		t.Fatalf("Up() after rollback error = %v", err)
	}
	assertMigrationVersion(t, inspectionDB, 2)
	assertColumnExists(t, inspectionDB, "documents", "published_revision_id", true)
	assertPublishedRevisionOwnership(t, inspectionDB)
}

func createTemporaryDatabase(t *testing.T, ctx context.Context, dsn string) (string, func()) {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test database DSN: %v", err)
	}
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open test database admin connection: %v", err)
	}
	databaseName := fmt.Sprintf("knowledge_migration_test_%d", time.Now().UTC().UnixNano())
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+identifier); err != nil {
		_ = adminDB.Close()
		t.Fatalf("create temporary migration database: %v", err)
	}
	config.Database = databaseName
	targetDSN := stdlib.RegisterConnConfig(config)
	return targetDSN, func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, dropErr := adminDB.ExecContext(dropCtx, "DROP DATABASE "+identifier+" WITH (FORCE)"); dropErr != nil {
			t.Errorf("drop temporary migration database: %v", dropErr)
		}
		if closeErr := adminDB.Close(); closeErr != nil {
			t.Errorf("close test database admin connection: %v", closeErr)
		}
		stdlib.UnregisterConnConfig(targetDSN)
	}
}

func openEmbeddedMigration(t *testing.T, dsn string) (*migrate.Migrate, func() error) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open rollback test database: %v", err)
	}
	driver, err := databasepostgres.WithInstance(db, &databasepostgres.Config{
		SchemaName:      schemaName,
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		_ = db.Close()
		t.Fatalf("create rollback test migration driver: %v", err)
	}
	source, err := iofs.New(knowledgemigrations.Files, ".")
	if err != nil {
		_ = db.Close()
		t.Fatalf("create rollback test migration source: %v", err)
	}
	migration, err := migrate.NewWithInstance("knowledge-embedded-test", source, schemaName, driver)
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		t.Fatalf("create rollback test migration runner: %v", err)
	}
	closed := false
	return migration, func() error {
		if closed {
			return nil
		}
		closed = true
		sourceErr, databaseErr := migration.Close()
		return errors.Join(sourceErr, databaseErr, db.Close())
	}
}

func assertMigrationVersion(t *testing.T, db *sql.DB, want uint) {
	t.Helper()
	var version uint
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM knowledge.schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != want || dirty {
		t.Fatalf("migration version = %d, dirty %t; want %d, false", version, dirty, want)
	}
}

func assertColumnExists(t *testing.T, db *sql.DB, table, column string, want bool) {
	t.Helper()
	var exists bool
	err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'knowledge' AND table_name = $1 AND column_name = $2
		)
	`, table, column).Scan(&exists)
	if err != nil || exists != want {
		t.Fatalf("column knowledge.%s.%s exists = %t, err %v; want %t", table, column, exists, err, want)
	}
}

func assertRelationExists(t *testing.T, db *sql.DB, relation string, want bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, relation).Scan(&exists); err != nil || exists != want {
		t.Fatalf("relation %s exists = %t, err %v; want %t", relation, exists, err, want)
	}
}

func assertPublishedRevisionOwnership(t *testing.T, db *sql.DB) {
	t.Helper()
	var firstDocumentID, secondDocumentID, revisionID int64
	if err := db.QueryRow(`
		INSERT INTO knowledge.documents (title, author_id) VALUES ('first', 1) RETURNING id
	`).Scan(&firstDocumentID); err != nil {
		t.Fatalf("insert first ownership test document: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO knowledge.documents (title, author_id) VALUES ('second', 2) RETURNING id
	`).Scan(&secondDocumentID); err != nil {
		t.Fatalf("insert second ownership test document: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO knowledge.document_revisions
			(document_id, version, title, summary, content_json, published_by)
		VALUES ($1, 0, 'first', '', '[]', 1)
		RETURNING id
	`, firstDocumentID).Scan(&revisionID); err != nil {
		t.Fatalf("insert ownership test revision: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE knowledge.documents SET published_revision_id = $1 WHERE id = $2
	`, revisionID, secondDocumentID); err == nil {
		t.Fatal("cross-document published revision reference succeeded")
	}
	if _, err := db.Exec(`
		UPDATE knowledge.documents
		SET status = 'published', published_revision_id = $1
		WHERE id = $2
	`, revisionID, firstDocumentID); err != nil {
		t.Fatalf("same-document published revision reference failed: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM knowledge.documents WHERE id = $1`, firstDocumentID); err != nil {
		t.Fatalf("delete document with published revision failed: %v", err)
	}
}

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	knowledgemigrations "github.com/HappyLadySauce/Knowledge-Core/migrations/knowledge/postgres"
	"github.com/golang-migrate/migrate/v4"
	databasepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const schemaName = "knowledge"

func Up(ctx context.Context, dsn string) (runErr error) {
	if dsn == "" {
		return errors.New("run knowledge migrations: database DSN is required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open knowledge migration database: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, db.Close())
	}()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping knowledge migration database: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS knowledge`); err != nil {
		return fmt.Errorf("create knowledge migration schema: %w", err)
	}

	driver, err := databasepostgres.WithInstance(db, &databasepostgres.Config{
		SchemaName:      schemaName,
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("create knowledge migration driver: %w", err)
	}
	source, err := iofs.New(knowledgemigrations.Files, ".")
	if err != nil {
		return fmt.Errorf("create knowledge migration source: %w", err)
	}
	migration, err := migrate.NewWithInstance("knowledge-embedded", source, schemaName, driver)
	if err != nil {
		return fmt.Errorf("create knowledge migration runner: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := migration.Close()
		if sourceErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close knowledge migration source: %w", sourceErr))
		}
		if databaseErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close knowledge migration database: %w", databaseErr))
		}
	}()

	err = migration.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("apply knowledge migrations: %w", err)
	}
	return nil
}

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identitymigrations "github.com/HappyLadySauce/Knowledge-Core/migrations/identity/postgres"
	"github.com/golang-migrate/migrate/v4"
	databasepostgres "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const schemaName = "identity"

func Up(ctx context.Context, dsn string) (runErr error) {
	if dsn == "" {
		return errors.New("run identity migrations: database DSN is required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open identity migration database: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, db.Close())
	}()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping identity migration database: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS identity`); err != nil {
		return fmt.Errorf("create identity migration schema: %w", err)
	}

	driver, err := databasepostgres.WithInstance(db, &databasepostgres.Config{
		SchemaName:      schemaName,
		MigrationsTable: "schema_migrations",
	})
	if err != nil {
		return fmt.Errorf("create identity migration driver: %w", err)
	}
	source, err := iofs.New(identitymigrations.Files, ".")
	if err != nil {
		return fmt.Errorf("create identity migration source: %w", err)
	}
	migration, err := migrate.NewWithInstance("identity-embedded", source, schemaName, driver)
	if err != nil {
		return fmt.Errorf("create identity migration runner: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := migration.Close()
		if sourceErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close identity migration source: %w", sourceErr))
		}
		if databaseErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close identity migration database: %w", databaseErr))
		}
	}()

	err = migration.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("apply identity migrations: %w", err)
	}
	return nil
}

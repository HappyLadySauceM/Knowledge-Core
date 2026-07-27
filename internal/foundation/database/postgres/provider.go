package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/database"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const providerName = "postgres"

type Provider struct{}

func NewProvider() *Provider {
	return &Provider{}
}

func (p *Provider) Name() string {
	return providerName
}

func (p *Provider) Open(ctx context.Context, cfg database.Config) (database.DB, error) {
	if cfg.DSN == "" {
		return nil, errors.New("open postgres: DSN is required")
	}

	db, err := sql.Open("pgx", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &sqlDB{DB: db}, nil
}

type sqlDB struct {
	*sql.DB
}

func (db *sqlDB) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	tx, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("begin postgres transaction: %w", err)
	}
	return tx, nil
}

var _ database.Provider = (*Provider)(nil)
var _ database.DB = (*sqlDB)(nil)

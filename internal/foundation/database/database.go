package database

import (
	"context"
	"database/sql"
	"time"
)

type Config struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

type Provider interface {
	Name() string
	Open(ctx context.Context, cfg Config) (DB, error)
}

type Executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type DB interface {
	Executor
	BeginTx(ctx context.Context, opts *sql.TxOptions) (Tx, error)
	PingContext(ctx context.Context) error
	Close() error
}

type Tx interface {
	Executor
	Commit() error
	Rollback() error
}

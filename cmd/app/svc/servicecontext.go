package svc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"
	"k8s.io/klog/v2"

	internalassets "github.com/HappyLadySauce/Knowledge-Core/internal/assets"
	"github.com/HappyLadySauce/Knowledge-Core/internal/auth"
	"github.com/HappyLadySauce/Knowledge-Core/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/localstorage"
)

// ServiceContext wires shared infrastructure for HTTP handlers and background work.
// ServiceContext 为 HTTP 处理器与后台任务提供共享的基础设施连接。
type ServiceContext struct {
	Config *config.Config
	DB     *sql.DB
	Redis  *redis.Client
	Auth   *auth.Service
	Assets *internalassets.Service
}

// NewServiceContext opens PostgreSQL and verifies the required schema.
// NewServiceContext 打开 PostgreSQL 并校验必要 schema。
func NewServiceContext(ctx context.Context, cfg *config.Config) (*ServiceContext, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if cfg.Database == nil {
		return nil, fmt.Errorf("database config is nil")
	}
	if cfg.JWT == nil {
		return nil, fmt.Errorf("jwt config is nil")
	}
	if cfg.Redis == nil {
		return nil, fmt.Errorf("redis config is nil")
	}
	if cfg.WebSocket == nil {
		return nil, fmt.Errorf("websocket config is nil")
	}
	if cfg.Uploads == nil {
		return nil, fmt.Errorf("uploads config is nil")
	}

	db, err := openPostgres(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := verifySchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	redisClient, err := openRedis(ctx, cfg)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	store, err := localstorage.New(cfg.Uploads.Dir)
	if err != nil {
		if redisClient != nil {
			_ = redisClient.Close()
		}
		_ = db.Close()
		return nil, fmt.Errorf("initialize local uploads: %w", err)
	}
	return &ServiceContext{
		Config: cfg,
		DB:     db,
		Redis:  redisClient,
		Auth:   auth.NewService(db, cfg.JWT, redisClient),
		Assets: internalassets.NewService(db, store, cfg.Uploads),
	}, nil
}

// Close releases database resources.
// Close 释放数据库资源。
func (s *ServiceContext) Close() error {
	var err error
	if s.DB != nil {
		err = errors.Join(err, s.DB.Close())
	}
	if s.Redis != nil {
		err = errors.Join(err, s.Redis.Close())
	}
	return err
}

func openPostgres(ctx context.Context, cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("pgx", cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

func openRedis(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	opts, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		klog.ErrorS(err, "redis unavailable; refresh token sessions will use postgres fallback")
		return nil, nil
	}
	return client, nil
}

func verifySchema(ctx context.Context, db *sql.DB) error {
	requiredTables := []string{
		"users", "refresh_tokens", "login_attempts", "documents", "document_blocks",
		"document_ops", "document_revisions", "categories", "tags", "document_tags",
		"assets",
	}
	for _, table := range requiredTables {
		var exists bool
		err := db.QueryRowContext(ctx, `SELECT to_regclass('public.`+table+`') IS NOT NULL`).Scan(&exists)
		if err != nil {
			return fmt.Errorf("verify postgres schema: %w", err)
		}
		if !exists {
			return fmt.Errorf("postgres schema is not migrated: missing table %s; run make migrate", table)
		}
	}
	requiredColumns := map[string][]string{
		"refresh_tokens": {"token_version", "last_used_at", "rotated_to_hash", "revoked_reason"},
	}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			var exists bool
			err := db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM information_schema.columns
    WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
)`, table, column).Scan(&exists)
			if err != nil {
				return fmt.Errorf("verify postgres schema columns: %w", err)
			}
			if !exists {
				return fmt.Errorf("postgres schema is not migrated: missing column %s.%s; run make migrate", table, column)
			}
		}
	}
	return nil
}

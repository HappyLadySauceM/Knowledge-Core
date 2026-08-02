package migration

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

const migrationLockKey int64 = 0x4b434b4e4f574d47

//go:embed migrations/*.sql
var files embed.FS

func Apply(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return errors.New("migrate knowledge schema: context and database are required")
	}
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list knowledge migrations: %w", err)
	}
	sort.Strings(names)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationLockKey).Error; err != nil {
			return fmt.Errorf("lock knowledge migrations: %w", err)
		}
		if err := tx.Exec(`CREATE SCHEMA IF NOT EXISTS knowledge;
CREATE TABLE IF NOT EXISTS knowledge.schema_migrations (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
)`).Error; err != nil {
			return fmt.Errorf("initialize knowledge migrations: %w", err)
		}
		for _, name := range names {
			version, parseErr := migrationVersion(name)
			if parseErr != nil {
				return parseErr
			}
			var applied bool
			if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM knowledge.schema_migrations WHERE version = ?)", version).Scan(&applied).Error; err != nil {
				return fmt.Errorf("check knowledge migration %d: %w", version, err)
			}
			if applied {
				continue
			}
			contents, readErr := files.ReadFile(name)
			if readErr != nil {
				return fmt.Errorf("read knowledge migration %q: %w", name, readErr)
			}
			if err := tx.Exec(string(contents)).Error; err != nil {
				return fmt.Errorf("apply knowledge migration %q: %w", name, err)
			}
			if err := tx.Exec("INSERT INTO knowledge.schema_migrations(version, name) VALUES (?, ?)", version, filepath.Base(name)).Error; err != nil {
				return fmt.Errorf("record knowledge migration %q: %w", name, err)
			}
		}
		return nil
	})
}

func migrationVersion(name string) (int64, error) {
	base := filepath.Base(name)
	prefix, _, ok := strings.Cut(base, "_")
	if !ok {
		return 0, fmt.Errorf("parse knowledge migration %q: expected numeric prefix", base)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("parse knowledge migration %q: invalid version", base)
	}
	return version, nil
}

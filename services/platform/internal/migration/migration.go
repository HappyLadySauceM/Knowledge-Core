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

//go:embed migrations/*.sql
var files embed.FS

const lockKey int64 = 0x504c4154464f524d

func Apply(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return errors.New("platform migration requires context and database")
	}
	names, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", lockKey).Error; err != nil {
			return err
		}
		if err := tx.Exec("CREATE SCHEMA IF NOT EXISTS platform; CREATE TABLE IF NOT EXISTS platform.schema_migrations(version bigint PRIMARY KEY,name text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())").Error; err != nil {
			return err
		}
		for _, name := range names {
			version, err := migrationVersion(name)
			if err != nil {
				return err
			}
			var applied bool
			if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM platform.schema_migrations WHERE version = ?)", version).Scan(&applied).Error; err != nil {
				return err
			}
			if applied {
				continue
			}
			contents, err := files.ReadFile(name)
			if err != nil {
				return err
			}
			if err := tx.Exec(string(contents)).Error; err != nil {
				return fmt.Errorf("apply %s: %w", name, err)
			}
			if err := tx.Exec("INSERT INTO platform.schema_migrations(version,name) VALUES (?,?)", version, filepath.Base(name)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func migrationVersion(name string) (int64, error) {
	prefix, _, ok := strings.Cut(filepath.Base(name), "_")
	if !ok {
		return 0, fmt.Errorf("invalid migration %s", name)
	}
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("invalid migration %s", name)
	}
	return version, nil
}

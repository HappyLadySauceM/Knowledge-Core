package migration

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var files embed.FS

const lockKey int64 = 0x4154544143484d54

func Apply(ctx context.Context, db *gorm.DB) error {
	if ctx == nil || db == nil {
		return errors.New("attachment migration requires context and database")
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
		if err := tx.Exec("CREATE SCHEMA IF NOT EXISTS attachment; CREATE TABLE IF NOT EXISTS attachment.schema_migrations(version bigint PRIMARY KEY,name text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())").Error; err != nil {
			return err
		}
		for _, name := range names {
			v, err := version(name)
			if err != nil {
				return err
			}
			var applied bool
			if err := tx.Raw("SELECT EXISTS (SELECT 1 FROM attachment.schema_migrations WHERE version = ?)", v).Scan(&applied).Error; err != nil {
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
			if err := tx.Exec("INSERT INTO attachment.schema_migrations(version,name) VALUES (?,?)", v, filepath.Base(name)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
func version(name string) (int64, error) {
	p, _, ok := strings.Cut(filepath.Base(name), "_")
	if !ok {
		return 0, fmt.Errorf("invalid migration %s", name)
	}
	v, err := strconv.ParseInt(p, 10, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid migration %s", name)
	}
	return v, nil
}

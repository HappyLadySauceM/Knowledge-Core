package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"gorm.io/gorm"
)

// migrationLockKey is stable across replicas and serializes Identity schema
// reconciliation during startup.
const migrationLockKey int64 = 0x4b4349444d494752

type constraint struct {
	name       string
	expression string
}

var userConstraints = []constraint{
	{name: "users_username_length_check", expression: "char_length(username) BETWEEN 3 AND 32"},
	{name: "users_role_check", expression: "role IN ('admin', 'user')"},
	{name: "users_status_check", expression: "status IN ('active', 'disabled')"},
	{name: "users_token_version_check", expression: "token_version >= 1"},
	{name: "users_failed_login_attempts_check", expression: "failed_login_attempts >= 0"},
}

// AutoMigrate performs the explicitly selected startup migration policy. GORM
// creates additive schema changes; PostgreSQL-specific constraints are then
// reconciled and verified while an advisory transaction lock is held.
func AutoMigrate(ctx context.Context, db *gorm.DB) error {
	if ctx == nil {
		return errors.New("migrate identity schema: context is required")
	}
	if db == nil {
		return errors.New("migrate identity schema: database is required")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationLockKey).Error; err != nil {
			return fmt.Errorf("lock identity schema migration: %w", err)
		}
		if err := tx.Exec("CREATE SCHEMA IF NOT EXISTS identity").Error; err != nil {
			return fmt.Errorf("create identity schema: %w", err)
		}
		if err := tx.AutoMigrate(&model.User{}); err != nil {
			return fmt.Errorf("auto-migrate identity users: %w", err)
		}
		for _, item := range userConstraints {
			if err := ensureConstraint(tx, item); err != nil {
				return err
			}
		}
		statements := []string{
			"CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_uidx ON identity.users (lower(username))",
			"CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uidx ON identity.users (lower(email))",
			"CREATE INDEX IF NOT EXISTS users_status_created_at_idx ON identity.users (status, created_at DESC)",
		}
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return fmt.Errorf("create identity index: %w", err)
			}
		}
		return verifySchema(tx)
	})
}

func ensureConstraint(tx *gorm.DB, item constraint) error {
	var exists bool
	if err := tx.Raw(`
SELECT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = ? AND conrelid = 'identity.users'::regclass
)`, item.name).Scan(&exists).Error; err != nil {
		return fmt.Errorf("check identity constraint %q: %w", item.name, err)
	}
	if exists {
		return nil
	}
	statement := fmt.Sprintf(
		"ALTER TABLE identity.users ADD CONSTRAINT %s CHECK (%s)",
		item.name,
		item.expression,
	)
	if err := tx.Exec(statement).Error; err != nil {
		return fmt.Errorf("create identity constraint %q: %w", item.name, err)
	}
	return nil
}

func verifySchema(tx *gorm.DB) error {
	for _, item := range userConstraints {
		var exists bool
		if err := tx.Raw(`
SELECT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = ? AND conrelid = 'identity.users'::regclass
)`, item.name).Scan(&exists).Error; err != nil {
			return fmt.Errorf("verify identity constraint %q: %w", item.name, err)
		}
		if !exists {
			return fmt.Errorf("verify identity schema: missing constraint %q", item.name)
		}
	}

	expectedIndexes := map[string]string{
		"users_username_lower_uidx": "lower(username)",
		"users_email_lower_uidx":    "lower(email)",
	}
	for name, expression := range expectedIndexes {
		var definition string
		if err := tx.Raw(`
SELECT pg_get_indexdef(indexrelid)
FROM pg_index
WHERE indexrelid = to_regclass(?) AND indisunique`, "identity."+name).Scan(&definition).Error; err != nil {
			return fmt.Errorf("verify identity index %q: %w", name, err)
		}
		if definition == "" || !strings.Contains(normalizeIndexDefinition(definition), normalizeIndexDefinition(expression)) {
			return fmt.Errorf("verify identity schema: index %q has unexpected definition", name)
		}
	}
	return nil
}

func normalizeIndexDefinition(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "::text", "")
	value = strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", "(", "", ")", "").Replace(value)
	return value
}

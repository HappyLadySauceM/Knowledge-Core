package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/database"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/jackc/pgx/v5/pgconn"
)

const userColumns = `
    id, username, email, password_hash, role, status, token_version,
    avatar, bio, failed_login_attempts, locked_until, created_at, updated_at`

// The stable database-wide key serializes bootstrap across service replicas.
const bootstrapAdminLockKey int64 = 0x4b43494441444d49

type UserRepository struct {
	db database.DB
}

func NewUserRepository(db database.DB) (*UserRepository, error) {
	if db == nil {
		return nil, errors.New("create postgres user repository: database is required")
	}
	return &UserRepository{db: db}, nil
}

func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	return insertUser(ctx, r.db, user)
}

func (r *UserRepository) CreateFirstAdmin(ctx context.Context, user *domain.User) (created bool, runErr error) {
	if user == nil {
		return false, errors.New("create first postgres administrator: user is required")
	}
	transaction, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin first identity administrator transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := transaction.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			runErr = errors.Join(runErr, fmt.Errorf("rollback first identity administrator transaction: %w", rollbackErr))
		}
	}()
	if _, err := transaction.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, bootstrapAdminLockKey); err != nil {
		return false, fmt.Errorf("lock first identity administrator creation: %w", err)
	}
	var exists bool
	if err := transaction.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM identity.users WHERE role = 'admin')`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check first identity administrator: %w", err)
	}
	if !exists {
		if err := insertUser(ctx, transaction, user); err != nil {
			return false, err
		}
		created = true
	}
	if err := transaction.Commit(); err != nil {
		return false, fmt.Errorf("commit first identity administrator transaction: %w", err)
	}
	committed = true
	return created, nil
}

func insertUser(ctx context.Context, executor database.Executor, user *domain.User) error {
	if user == nil {
		return errors.New("create postgres user: user is required")
	}
	query := `
        INSERT INTO identity.users (username, email, password_hash, role, status, token_version, avatar, bio)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        RETURNING id, created_at, updated_at`
	err := executor.QueryRowContext(ctx, query,
		user.Username,
		user.Email,
		user.PasswordHash,
		user.Role,
		user.Status,
		user.TokenVersion,
		user.Avatar,
		user.Bio,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		switch postgresError.ConstraintName {
		case "users_username_lower_uidx":
			return repository.ErrUsernameConflict
		case "users_email_lower_uidx":
			return repository.ErrEmailConflict
		}
	}
	return fmt.Errorf("insert identity user: %w", err)
}

func (r *UserRepository) CountAdmins(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM identity.users WHERE role = 'admin'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count identity administrators: %w", err)
	}
	return count, nil
}

func (r *UserRepository) FindByID(ctx context.Context, id int64) (*domain.User, error) {
	query := `SELECT ` + userColumns + ` FROM identity.users WHERE id = $1`
	return scanUser(r.db.QueryRowContext(ctx, query, id))
}

func (r *UserRepository) FindByLogin(ctx context.Context, identifier string) (*domain.User, error) {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	query := `SELECT ` + userColumns + `
        FROM identity.users
        WHERE lower(username) = $1 OR lower(email) = $1`
	return scanUser(r.db.QueryRowContext(ctx, query, identifier))
}

func (r *UserRepository) RecordLoginFailure(
	ctx context.Context,
	id int64,
	failedAt time.Time,
	lockUntil time.Time,
	threshold int,
) (bool, error) {
	query := `
        UPDATE identity.users
        SET failed_login_attempts = CASE
                WHEN locked_until IS NOT NULL AND locked_until <= $2::timestamptz THEN 1
                ELSE failed_login_attempts + 1
            END,
            locked_until = CASE
                WHEN (CASE
                    WHEN locked_until IS NOT NULL AND locked_until <= $2::timestamptz THEN 1
                    ELSE failed_login_attempts + 1
                END) >= $4 THEN $3::timestamptz
                ELSE NULL
            END
        WHERE id = $1
        RETURNING locked_until IS NOT NULL AND locked_until > $2::timestamptz`
	var locked bool
	if err := r.db.QueryRowContext(ctx, query, id, failedAt, lockUntil, threshold).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, repository.ErrUserNotFound
		}
		return false, fmt.Errorf("update identity login failure: %w", err)
	}
	return locked, nil
}

func (r *UserRepository) CompleteLoginSuccess(ctx context.Context, id int64, completedAt time.Time) (user *domain.User, runErr error) {
	transaction, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin identity login success transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := transaction.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			runErr = errors.Join(runErr, fmt.Errorf("rollback identity login success transaction: %w", rollbackErr))
		}
	}()

	query := `SELECT ` + userColumns + ` FROM identity.users WHERE id = $1 FOR UPDATE`
	user, err = scanUser(transaction.QueryRowContext(ctx, query, id))
	if err != nil {
		return nil, err
	}
	if user.Status == domain.StatusActive && !user.IsLocked(completedAt) {
		query = `
            UPDATE identity.users
            SET failed_login_attempts = 0, locked_until = NULL
            WHERE id = $1
            RETURNING ` + userColumns
		user, err = scanUser(transaction.QueryRowContext(ctx, query, id))
		if err != nil {
			return nil, fmt.Errorf("reset identity login failures: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit identity login success transaction: %w", err)
	}
	committed = true
	return user, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*domain.User, error) {
	user := &domain.User{}
	err := row.Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.PasswordHash,
		&user.Role,
		&user.Status,
		&user.TokenVersion,
		&user.Avatar,
		&user.Bio,
		&user.FailedLoginAttempts,
		&user.LockedUntil,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, repository.ErrUserNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan identity user: %w", err)
	}
	return user, nil
}

var _ repository.UserRepository = (*UserRepository)(nil)

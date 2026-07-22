package user

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/postgres"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, username, email, passwordHash string) (User, error) {
	return r.CreateWithRole(ctx, username, email, passwordHash, RoleUser, false)
}

// CreateWithRole creates a user with an explicit role (used for admin bootstrap).
// CreateWithRole 使用显式角色创建用户（用于 admin 引导）。
func (r *Repository) CreateWithRole(ctx context.Context, username, email, passwordHash, role string, mustChangePassword bool) (User, error) {
	now := time.Now().UTC()
	var id int64
	err := r.db.QueryRowContext(ctx, `
INSERT INTO users (username, email, avatar, bio, password_hash, role, status, must_change_password, created_at, updated_at)
VALUES ($1, NULLIF($2, ''), '', '', $3, $4, $5, $6, $7, $8)
RETURNING id`,
		username, email, passwordHash, role, StatusActive, mustChangePassword, now, now).Scan(&id)
	if err != nil {
		if postgres.IsUniqueViolation(err) {
			return User{}, apperrors.Conflict
		}
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return r.GetByID(ctx, id)
}

func (r *Repository) GetByID(ctx context.Context, id int64) (User, error) {
	row := r.db.QueryRowContext(ctx, userSelectSQL+` WHERE id = $1`, id)
	return scanUser(row)
}

func (r *Repository) GetRecordByID(ctx context.Context, id int64) (Record, error) {
	row := r.db.QueryRowContext(ctx, recordSelectSQL+` WHERE id = $1`, id)
	return scanRecord(row, apperrors.NotFound)
}

func (r *Repository) GetRecordByUsername(ctx context.Context, username string) (Record, error) {
	row := r.db.QueryRowContext(ctx, recordSelectSQL+` WHERE username = $1`, username)
	return scanRecord(row, apperrors.InvalidCredentials)
}

func (r *Repository) List(ctx context.Context, query ListQuery) (ListResult, error) {
	where, args := listWhere(query)
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return ListResult{}, apperrors.Wrap(apperrors.InternalError, err)
	}

	offset := (query.Page - 1) * query.PageSize
	args = append(args, query.PageSize, offset)
	rows, err := r.db.QueryContext(ctx, userSelectSQL+where+`
ORDER BY id ASC
LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return ListResult{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	defer rows.Close()

	items := make([]User, 0)
	for rows.Next() {
		item, err := scanUser(rows)
		if err != nil {
			return ListResult{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return ListResult{Items: items, Total: total, Page: query.Page, PageSize: query.PageSize}, nil
}

func (r *Repository) UpdateProfile(ctx context.Context, id int64, cmd UpdateProfileCommand) (User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	defer rollbackTx(tx)

	current, err := scanUser(tx.QueryRowContext(ctx, userSelectSQL+` WHERE id = $1`, id))
	if err != nil {
		return User{}, err
	}
	nextUsername, nextEmail, nextAvatar, nextBio := profileValues(current, cmd)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE users
SET username = $1, email = NULLIF($2, ''), avatar = $3, bio = $4, updated_at = $5
WHERE id = $6`,
		nextUsername, nextEmail, nextAvatar, nextBio, now, id); err != nil {
		if postgres.IsUniqueViolation(err) {
			return User{}, apperrors.Conflict
		}
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return r.GetByID(ctx, id)
}

func (r *Repository) AdminUpdate(ctx context.Context, id int64, cmd AdminUpdateCommand) (User, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	defer rollbackTx(tx)

	current, err := scanUser(tx.QueryRowContext(ctx, userSelectSQL+` WHERE id = $1`, id))
	if err != nil {
		return User{}, err
	}
	nextUsername, nextEmail, nextAvatar, nextBio := adminProfileValues(current, cmd)
	nextStatus := current.Status
	nextRole := current.Role
	if cmd.Status != nil {
		nextStatus = *cmd.Status
	}
	if cmd.Role != nil {
		nextRole = *cmd.Role
	}
	if err := protectLastActiveAdmin(ctx, tx, current, nextStatus, nextRole); err != nil {
		return User{}, err
	}

	now := time.Now().UTC()
	invalidateTokens := nextStatus != current.Status || nextRole != current.Role
	if _, err := tx.ExecContext(ctx, `
UPDATE users
SET username = $1, email = NULLIF($2, ''), avatar = $3, bio = $4, status = $5, role = $6,
    token_version = CASE WHEN $7 THEN token_version + 1 ELSE token_version END,
    updated_at = $8
WHERE id = $9`,
		nextUsername, nextEmail, nextAvatar, nextBio, nextStatus, nextRole, invalidateTokens, now, id); err != nil {
		if postgres.IsUniqueViolation(err) {
			return User{}, apperrors.Conflict
		}
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return r.GetByID(ctx, id)
}

func (r *Repository) Disable(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	defer rollbackTx(tx)

	current, err := scanUser(tx.QueryRowContext(ctx, userSelectSQL+` WHERE id = $1`, id))
	if err != nil {
		return err
	}
	if err := protectLastActiveAdmin(ctx, tx, current, StatusDisabled, current.Role); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE users
SET status = $1, token_version = token_version + 1, updated_at = $2
WHERE id = $3`, StatusDisabled, now, id); err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	return nil
}

func (r *Repository) UpdatePasswordHash(ctx context.Context, id int64, passwordHash string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	defer rollbackTx(tx)

	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
UPDATE users
SET password_hash = $1, must_change_password = false, token_version = token_version + 1, updated_at = $2
WHERE id = $3`, passwordHash, now, id)
	if err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	if affected == 0 {
		return apperrors.NotFound
	}
	if err := tx.Commit(); err != nil {
		return apperrors.Wrap(apperrors.InternalError, err)
	}
	return nil
}

func listWhere(query ListQuery) (string, []any) {
	parts := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if query.Role != "" {
		parts = append(parts, "role = $"+strconv.Itoa(len(args)+1))
		args = append(args, query.Role)
	}
	if query.Status != "" {
		parts = append(parts, "status = $"+strconv.Itoa(len(args)+1))
		args = append(args, query.Status)
	}
	if query.Keyword != "" {
		// Prefix match (LIKE 'kw%') can use the username/email unique indexes,
		// avoiding the full-table scan of LIKE '%kw%'.
		// 前缀匹配（LIKE 'kw%'）可使用 username/email 唯一索引，
		// 避免 LIKE '%kw%' 的全表扫描。
		parts = append(parts, "(username LIKE $"+strconv.Itoa(len(args)+1)+" OR email LIKE $"+strconv.Itoa(len(args)+2)+")")
		prefix := query.Keyword + "%"
		args = append(args, prefix, prefix)
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func profileValues(current User, cmd UpdateProfileCommand) (string, string, string, string) {
	username, email, avatar, bio := current.Username, current.Email, current.Avatar, current.Bio
	if cmd.Username != nil {
		username = *cmd.Username
	}
	if cmd.Email != nil {
		email = *cmd.Email
	}
	if cmd.Avatar != nil {
		avatar = *cmd.Avatar
	}
	if cmd.Bio != nil {
		bio = *cmd.Bio
	}
	return username, email, avatar, bio
}

func adminProfileValues(current User, cmd AdminUpdateCommand) (string, string, string, string) {
	return profileValues(current, UpdateProfileCommand{
		Username: cmd.Username,
		Email:    cmd.Email,
		Avatar:   cmd.Avatar,
		Bio:      cmd.Bio,
	})
}

func protectLastActiveAdmin(ctx context.Context, tx *sql.Tx, current User, nextStatus, nextRole string) error {
	if current.Role != RoleAdmin || current.Status != StatusActive {
		return nil
	}
	if nextStatus == StatusActive && nextRole == RoleAdmin {
		return nil
	}
	count, err := countActiveAdmins(ctx, tx)
	if err != nil {
		return err
	}
	if count <= 1 {
		return apperrors.Forbidden
	}
	return nil
}

func countActiveAdmins(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int64, error) {
	var count int64
	err := queryer.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM users
WHERE role = $1 AND status = $2`, RoleAdmin, StatusActive).Scan(&count)
	if err != nil {
		return 0, apperrors.Wrap(apperrors.InternalError, err)
	}
	return count, nil
}

const userSelectSQL = `
SELECT id, username, COALESCE(email, ''), avatar, bio, role, status, must_change_password, token_version, created_at, updated_at
FROM users`

const recordSelectSQL = `
SELECT id, username, COALESCE(email, ''), avatar, bio, password_hash, role, status, must_change_password, token_version, created_at, updated_at
FROM users`

func scanUser(row interface {
	Scan(dest ...any) error
}) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Avatar, &u.Bio, &u.Role, &u.Status, &u.MustChangePassword, &u.TokenVersion, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, apperrors.NotFound
		}
		return User{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return u, nil
}

func scanRecord(row interface {
	Scan(dest ...any) error
}, missing error) (Record, error) {
	var record Record
	err := row.Scan(&record.ID, &record.Username, &record.Email, &record.Avatar, &record.Bio, &record.PasswordHash, &record.Role, &record.Status, &record.MustChangePassword, &record.TokenVersion, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, missing
		}
		return Record{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return record, nil
}

func rollbackTx(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		// Rollback failures after a failed transaction are not actionable here.
		// 事务失败后的回滚错误在此处不可恢复。
		return
	}
}

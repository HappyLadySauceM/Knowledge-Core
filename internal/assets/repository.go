package assets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Create(ctx context.Context, asset Asset) (Asset, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `
INSERT INTO assets (
    storage_key, original_name, content_type, size_bytes, sha256,
    status, created_by, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, 0), $8, $9)
RETURNING id`,
		asset.StorageKey, asset.OriginalName, asset.ContentType, asset.SizeBytes, asset.SHA256,
		asset.Status, asset.CreatedBy, asset.CreatedAt, asset.UpdatedAt).Scan(&id)
	if err != nil {
		return Asset{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return r.GetByID(ctx, id)
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Asset, error) {
	row := r.db.QueryRowContext(ctx, assetSelectSQL+` WHERE id = $1`, id)
	return scanAsset(row)
}

func (r *Repository) GetReadyByID(ctx context.Context, id int64) (Asset, error) {
	row := r.db.QueryRowContext(ctx, assetSelectSQL+` WHERE id = $1 AND status = $2`, id, StatusReady)
	return scanAsset(row)
}

func (r *Repository) MarkDeleted(ctx context.Context, id int64, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE assets
SET status = $1, updated_at = $2
WHERE id = $3 AND status = $4`, StatusDeleted, now, id, StatusReady)
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
	return nil
}

const assetSelectSQL = `
SELECT id, storage_key, original_name, content_type, size_bytes, sha256,
       status, COALESCE(created_by, 0), created_at, updated_at
FROM assets`

func scanAsset(row interface {
	Scan(dest ...any) error
}) (Asset, error) {
	var asset Asset
	err := row.Scan(
		&asset.ID, &asset.StorageKey, &asset.OriginalName, &asset.ContentType,
		&asset.SizeBytes, &asset.SHA256, &asset.Status, &asset.CreatedBy,
		&asset.CreatedAt, &asset.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Asset{}, apperrors.NotFound
		}
		return Asset{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	return asset, nil
}

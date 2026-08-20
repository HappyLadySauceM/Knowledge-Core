package repository

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrSessionNotFound = errors.New("identity repository: session not found")
	ErrSessionInvalid  = errors.New("identity repository: session is invalid")
	ErrSessionReplay   = errors.New("identity repository: refresh token replay detected")
)

type SessionRepository interface {
	Create(context.Context, *domain.Session) error
	Find(context.Context, string) (*domain.Session, error)
	Rotate(context.Context, string, []byte, []byte, time.Time, time.Time) (*domain.Session, error)
	Revoke(context.Context, string, string, time.Time) error
	RevokeAll(context.Context, int64, string, time.Time) error
	List(context.Context, int64) ([]*domain.Session, error)
}

type postgresSessionRepository struct{ db *gorm.DB }

func NewSessionRepository(db *gorm.DB) (SessionRepository, error) {
	if db == nil {
		return nil, errors.New("create postgres session repository: database is required")
	}
	return &postgresSessionRepository{db: db}, nil
}

func (r *postgresSessionRepository) Create(ctx context.Context, session *domain.Session) error {
	if session == nil || session.ID == "" || session.UserID <= 0 || len(session.RefreshDigest) == 0 {
		return errors.New("create identity session: session is invalid")
	}
	record := &model.Session{ID: session.ID, UserID: session.UserID, DeviceLabel: session.DeviceLabel, RefreshDigest: session.RefreshDigest, CreatedAt: session.CreatedAt.UTC(), LastSeenAt: session.LastSeenAt.UTC(), ExpiresAt: session.ExpiresAt.UTC()}
	if err := r.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("insert identity session: %w", err)
	}
	applySessionModel(session, record)
	return nil
}

func (r *postgresSessionRepository) Find(ctx context.Context, id string) (*domain.Session, error) {
	var record model.Session
	if err := r.db.WithContext(ctx).First(&record, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("find identity session: %w", err)
	}
	return fromSessionModel(&record), nil
}

func (r *postgresSessionRepository) Rotate(ctx context.Context, id string, currentDigest, nextDigest []byte, now, expires time.Time) (*domain.Session, error) {
	var session *domain.Session
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record model.Session
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSessionNotFound
			}
			return fmt.Errorf("lock identity session: %w", err)
		}
		if record.RevokedAt != nil || !record.ExpiresAt.After(now.UTC()) {
			return ErrSessionInvalid
		}
		if subtle.ConstantTimeCompare(record.RefreshDigest, currentDigest) != 1 {
			revokeAt := now.UTC()
			if err := tx.Model(&record).Updates(map[string]any{"revoked_at": revokeAt, "revoked_reason": "refresh_replay"}).Error; err != nil {
				return fmt.Errorf("revoke replayed identity session: %w", err)
			}
			return ErrSessionReplay
		}
		rotatedAt := now.UTC()
		if err := tx.Model(&record).Updates(map[string]any{"previous_digest": record.RefreshDigest, "refresh_digest": nextDigest, "last_seen_at": rotatedAt, "expires_at": expires.UTC(), "rotated_at": rotatedAt}).Error; err != nil {
			return fmt.Errorf("rotate identity session: %w", err)
		}
		record.PreviousDigest, record.RefreshDigest, record.LastSeenAt, record.ExpiresAt, record.RotatedAt = record.RefreshDigest, nextDigest, rotatedAt, expires.UTC(), &rotatedAt
		session = fromSessionModel(&record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (r *postgresSessionRepository) Revoke(ctx context.Context, id, reason string, now time.Time) error {
	result := r.db.WithContext(ctx).Model(&model.Session{}).Where("id = ? AND revoked_at IS NULL", id).Updates(map[string]any{"revoked_at": now.UTC(), "revoked_reason": reason})
	if result.Error != nil {
		return fmt.Errorf("revoke identity session: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (r *postgresSessionRepository) RevokeAll(ctx context.Context, userID int64, reason string, now time.Time) error {
	if userID <= 0 {
		return errors.New("revoke identity sessions: user ID is invalid")
	}
	if err := r.db.WithContext(ctx).Model(&model.Session{}).Where("user_id = ? AND revoked_at IS NULL", userID).Updates(map[string]any{"revoked_at": now.UTC(), "revoked_reason": reason}).Error; err != nil {
		return fmt.Errorf("revoke identity sessions: %w", err)
	}
	return nil
}

func (r *postgresSessionRepository) List(ctx context.Context, userID int64) ([]*domain.Session, error) {
	var records []model.Session
	if err := r.db.WithContext(ctx).Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, time.Now().UTC()).Order("last_seen_at DESC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list identity sessions: %w", err)
	}
	result := make([]*domain.Session, 0, len(records))
	for index := range records {
		result = append(result, fromSessionModel(&records[index]))
	}
	return result, nil
}

func fromSessionModel(record *model.Session) *domain.Session {
	if record == nil {
		return nil
	}
	return &domain.Session{ID: record.ID, UserID: record.UserID, DeviceLabel: record.DeviceLabel, RefreshDigest: append([]byte(nil), record.RefreshDigest...), PreviousDigest: append([]byte(nil), record.PreviousDigest...), CreatedAt: record.CreatedAt, LastSeenAt: record.LastSeenAt, ExpiresAt: record.ExpiresAt, RotatedAt: record.RotatedAt, RevokedAt: record.RevokedAt, RevokedReason: record.RevokedReason}
}
func applySessionModel(session *domain.Session, record *model.Session) {
	*session = *fromSessionModel(record)
}

var _ SessionRepository = (*postgresSessionRepository)(nil)

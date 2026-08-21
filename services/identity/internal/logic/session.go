package logic

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	coreauth "github.com/HappyLadySauce/Knowledge-Core/pkg/auth"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
)

type SessionAuthentication struct {
	User         *domain.User
	AccessToken  coreauth.IssuedToken
	RefreshToken string
	SessionID    string
}

type SessionLogic struct {
	users interface {
		FindByID(context.Context, int64) (*domain.User, error)
	}
	sessions     repository.SessionRepository
	issuer       AccessTokenIssuer
	pepper       []byte
	absoluteTTL  time.Duration
	idleTTL      time.Duration
	refreshGrace time.Duration
	now          func() time.Time
}

func NewSessionLogic(users interface {
	FindByID(context.Context, int64) (*domain.User, error)
}, sessions repository.SessionRepository, issuer AccessTokenIssuer, pepper string, absoluteTTL, idleTTL time.Duration) (*SessionLogic, error) {
	if users == nil || sessions == nil || issuer == nil || len(pepper) < 16 || absoluteTTL <= 0 || idleTTL <= 0 {
		return nil, errors.New("create identity session logic: dependencies and TTLs are required")
	}
	return &SessionLogic{users: users, sessions: sessions, issuer: issuer, pepper: []byte(pepper), absoluteTTL: absoluteTTL, idleTTL: idleTTL, refreshGrace: 10 * time.Second, now: time.Now}, nil
}

func (l *SessionLogic) Create(ctx context.Context, user *domain.User, deviceLabel string) (*SessionAuthentication, error) {
	if user == nil || user.ID <= 0 || user.Status != domain.StatusActive {
		return nil, identityerrors.UserDisabled.New()
	}
	now := l.now().UTC()
	refresh, err := security.NewRefreshToken(nil)
	if err != nil {
		return nil, fmt.Errorf("create identity refresh token: %w", err)
	}
	encoded, err := refresh.Encode()
	if err != nil {
		return nil, err
	}
	expires := now.Add(l.absoluteTTL)
	if idle := now.Add(l.idleTTL); idle.Before(expires) {
		expires = idle
	}
	session := &domain.Session{ID: refresh.SessionID, UserID: user.ID, DeviceLabel: normalizeDeviceLabel(deviceLabel), RefreshDigest: security.DigestRefreshSecret(refresh.Secret, l.pepper), CurrentRefreshToken: encoded, CreatedAt: now, LastSeenAt: now, ExpiresAt: expires}
	if err := l.sessions.Create(ctx, session); err != nil {
		return nil, fmt.Errorf("persist identity session: %w", err)
	}
	access, err := l.issuer.Issue(coreauth.Principal{UserID: user.ID, Role: user.Role, TokenVersion: user.TokenVersion, SessionID: session.ID})
	if err != nil {
		return nil, fmt.Errorf("issue identity access token: %w", err)
	}
	return &SessionAuthentication{User: user, AccessToken: access, RefreshToken: encoded, SessionID: session.ID}, nil
}

func (l *SessionLogic) Refresh(ctx context.Context, encoded string) (*SessionAuthentication, error) {
	refresh, err := security.ParseRefreshToken(strings.TrimSpace(encoded))
	if err != nil {
		return nil, identityerrors.Unauthenticated.Wrap(err)
	}
	now := l.now().UTC()
	session, err := l.sessions.Find(ctx, refresh.SessionID)
	if err != nil {
		return nil, identityerrors.Unauthenticated.Wrap(err)
	}
	if session == nil || session.UserID <= 0 || !session.IsActive(now) {
		return nil, identityerrors.Unauthenticated.New()
	}
	user, err := l.users.FindByID(ctx, session.UserID)
	if err != nil {
		return nil, identityerrors.Unauthenticated.Wrap(err)
	}
	if user == nil || user.Status != domain.StatusActive {
		return nil, identityerrors.UserDisabled.New()
	}
	next, err := security.NewRefreshToken(nil)
	if err != nil {
		return nil, fmt.Errorf("rotate identity refresh token: %w", err)
	}
	nextEncoded, err := next.Encode()
	if err != nil {
		return nil, err
	}
	nextExpires := session.CreatedAt.UTC().Add(l.absoluteTTL)
	if idle := now.Add(l.idleTTL); idle.Before(nextExpires) {
		nextExpires = idle
	}
	var rotated *domain.Session
	if graceful, ok := l.sessions.(interface {
		RotateWithGrace(context.Context, string, []byte, []byte, string, time.Time, time.Time, time.Duration) (*domain.Session, error)
	}); ok {
		rotated, err = graceful.RotateWithGrace(ctx, session.ID, security.DigestRefreshSecret(refresh.Secret, l.pepper), security.DigestRefreshSecret(next.Secret, l.pepper), nextEncoded, now, nextExpires, l.refreshGrace)
	} else {
		rotated, err = l.sessions.Rotate(ctx, session.ID, security.DigestRefreshSecret(refresh.Secret, l.pepper), security.DigestRefreshSecret(next.Secret, l.pepper), now, nextExpires)
	}
	if err != nil {
		if errors.Is(err, repository.ErrSessionReplay) {
			return nil, identityerrors.Unauthenticated.Wrap(err)
		}
		return nil, identityerrors.Unauthenticated.Wrap(err)
	}
	access, err := l.issuer.Issue(coreauth.Principal{UserID: user.ID, Role: user.Role, TokenVersion: user.TokenVersion, SessionID: rotated.ID})
	if err != nil {
		return nil, fmt.Errorf("issue refreshed identity access token: %w", err)
	}
	refreshToken := nextEncoded
	if rotated.CurrentRefreshToken != "" {
		refreshToken = rotated.CurrentRefreshToken
	}
	return &SessionAuthentication{User: user, AccessToken: access, RefreshToken: refreshToken, SessionID: rotated.ID}, nil
}

func (l *SessionLogic) List(ctx context.Context, userID int64) ([]*domain.Session, error) {
	if userID <= 0 {
		return nil, identityerrors.InvalidInput.New()
	}
	return l.sessions.List(ctx, userID)
}

func (l *SessionLogic) Revoke(ctx context.Context, userID int64, sessionID string, reason string) error {
	if userID <= 0 || strings.TrimSpace(sessionID) == "" {
		return identityerrors.InvalidInput.New()
	}
	session, err := l.sessions.Find(ctx, strings.TrimSpace(sessionID))
	if err != nil || session == nil || session.UserID != userID {
		return identityerrors.UserNotFound.New()
	}
	if err := l.sessions.Revoke(ctx, session.ID, reason, l.now().UTC()); err != nil {
		return identityerrors.UserNotFound.Wrap(err)
	}
	return nil
}

func (l *SessionLogic) RevokeAll(ctx context.Context, userID int64, reason string) error {
	if userID <= 0 {
		return identityerrors.InvalidInput.New()
	}
	if err := l.sessions.RevokeAll(ctx, userID, reason, l.now().UTC()); err != nil {
		return fmt.Errorf("revoke identity sessions: %w", err)
	}
	return nil
}

func normalizeDeviceLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unknown device"
	}
	if len(value) > 120 {
		return value[:120]
	}
	return value
}

func SessionPepper(privateKey string) string {
	digest := sha256.Sum256([]byte(privateKey))
	return string(digest[:])
}

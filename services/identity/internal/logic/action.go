package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/domain"
	identityerrors "github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
	"github.com/google/uuid"
)

type ActionLogic struct {
	users     repository.UserRepository
	actions   repository.ActionRepository
	sessions  repository.SessionRepository
	passwords PasswordVerifier
	pepper    string
	enqueue   func(context.Context, domain.EmailMessage) error
	now       func() time.Time
	ttl       time.Duration
}

func NewActionLogic(users repository.UserRepository, actions repository.ActionRepository, sessions repository.SessionRepository, passwords PasswordVerifier, pepper string, ttl time.Duration, enqueue func(context.Context, domain.EmailMessage) error) (*ActionLogic, error) {
	if users == nil || actions == nil || passwords == nil || len(pepper) < 16 || ttl <= 0 {
		return nil, errors.New("create identity action logic: dependencies and TTL are required")
	}
	if sessions == nil {
		return nil, errors.New("create identity action logic: session repository is required")
	}
	return &ActionLogic{users: users, actions: actions, sessions: sessions, passwords: passwords, pepper: pepper, ttl: ttl, enqueue: enqueue, now: time.Now}, nil
}

func (l *ActionLogic) issue(ctx context.Context, user *domain.User, kind, subject string) error {
	if user == nil || user.ID <= 0 {
		return identityerrors.UserNotFound.New()
	}
	token, err := security.NewActionToken()
	if err != nil {
		return fmt.Errorf("generate identity action token: %w", err)
	}
	now := l.now().UTC()
	entry := &domain.ActionToken{ID: uuid.NewString(), UserID: user.ID, Kind: kind, Digest: security.DigestActionToken(token, l.pepper), ExpiresAt: now.Add(l.ttl), CreatedAt: now}
	message := domain.EmailMessage{Kind: kind, To: user.Email, Subject: subject, Token: token, CreatedAt: now}
	if atomic, ok := l.actions.(interface {
		CreateAndEnqueue(context.Context, *domain.ActionToken, domain.EmailMessage) error
	}); ok {
		if err := atomic.CreateAndEnqueue(ctx, entry, message); err != nil {
			return fmt.Errorf("persist identity action email: %w", err)
		}
	} else if err := l.actions.Create(ctx, entry); err != nil {
		return fmt.Errorf("persist identity action token: %w", err)
	} else if l.enqueue != nil {
		if err := l.enqueue(ctx, message); err != nil {
			return fmt.Errorf("queue identity email: %w", err)
		}
	}
	return nil
}

func (l *ActionLogic) RequestEmailVerification(ctx context.Context, email string) error {
	user, err := l.users.FindByLogin(ctx, strings.TrimSpace(email))
	if err != nil || user == nil || user.EmailVerifiedAt != nil || user.Status == domain.StatusDisabled {
		return nil
	}
	return l.issue(ctx, user, domain.ActionEmailVerification, "Verify your email")
}

func (l *ActionLogic) VerifyEmail(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return identityerrors.InvalidInput.New()
	}
	entry, err := l.actions.Consume(ctx, domain.ActionEmailVerification, security.DigestActionToken(token, l.pepper), l.now().UTC())
	if err != nil {
		return identityerrors.InvalidInput.Wrap(err)
	}
	if _, err := l.users.MarkEmailVerified(ctx, entry.UserID, l.now().UTC()); err != nil {
		return identityerrors.InvalidInput.Wrap(err)
	}
	return nil
}

func (l *ActionLogic) RequestPasswordReset(ctx context.Context, identifier string) error {
	user, err := l.users.FindByLogin(ctx, strings.TrimSpace(identifier))
	if err != nil || user == nil || user.Status == domain.StatusDisabled {
		return nil
	}
	return l.issue(ctx, user, domain.ActionPasswordReset, "Reset your password")
}

func (l *ActionLogic) ResetPassword(ctx context.Context, token, password string) error {
	if err := domain.ValidatePassword(password); err != nil {
		return identityerrors.InvalidInput.Wrap(err)
	}
	entry, err := l.actions.Consume(ctx, domain.ActionPasswordReset, security.DigestActionToken(strings.TrimSpace(token), l.pepper), l.now().UTC())
	if err != nil {
		return identityerrors.InvalidInput.Wrap(err)
	}
	hash, err := l.passwords.Hash(password)
	if err != nil {
		return fmt.Errorf("hash identity reset password: %w", err)
	}
	if _, err := l.users.UpdatePassword(ctx, entry.UserID, hash, l.now().UTC()); err != nil {
		return identityerrors.InvalidInput.Wrap(err)
	}
	return nil
}

func (l *ActionLogic) Deactivate(ctx context.Context, userID int64, password string) error {
	user, err := l.users.FindByID(ctx, userID)
	if err != nil || user == nil {
		return identityerrors.Unauthenticated.New()
	}
	matched, err := l.passwords.Compare(user.PasswordHash, password)
	if err != nil {
		return fmt.Errorf("compare identity deactivation password: %w", err)
	}
	if !matched {
		return identityerrors.InvalidCredentials.New()
	}
	if err := l.users.Deactivate(ctx, userID, l.now().UTC()); err != nil {
		return fmt.Errorf("deactivate identity account: %w", err)
	}
	if err := l.sessions.RevokeAll(ctx, userID, "account_deactivated", l.now().UTC()); err != nil {
		return fmt.Errorf("revoke deactivated identity sessions: %w", err)
	}
	return nil
}

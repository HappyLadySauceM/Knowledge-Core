package domain

import "time"

const (
	ActionEmailVerification = "email_verification"
	ActionPasswordReset     = "password_reset"
)

type ActionToken struct {
	ID        string
	UserID    int64
	Kind      string
	Digest    []byte
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

func (t *ActionToken) IsActive(now time.Time) bool {
	return t != nil && t.UsedAt == nil && t.ExpiresAt.After(now.UTC())
}

type EmailMessage struct {
	Kind      string
	To        string
	Subject   string
	Token     string
	CreatedAt time.Time
}

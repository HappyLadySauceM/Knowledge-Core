package domain

import "time"

type Session struct {
	ID             string
	UserID         int64
	DeviceLabel    string
	RefreshDigest  []byte
	PreviousDigest []byte
	// CurrentRefreshToken is transient and is only returned for a refresh
	// request that reuses the previous token during the grace window.
	CurrentRefreshToken string
	CreatedAt           time.Time
	LastSeenAt          time.Time
	ExpiresAt           time.Time
	RotatedAt           *time.Time
	RevokedAt           *time.Time
	RevokedReason       string
}

func (s *Session) IsActive(now time.Time) bool {
	return s != nil && s.RevokedAt == nil && s.ExpiresAt.After(now.UTC())
}

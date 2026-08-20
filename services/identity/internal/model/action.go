package model

import "time"

type ActionToken struct {
	ID        string     `gorm:"primaryKey;size:36"`
	UserID    int64      `gorm:"not null;index"`
	Kind      string     `gorm:"size:32;not null;index"`
	Digest    []byte     `gorm:"type:bytea;not null;uniqueIndex"`
	ExpiresAt time.Time  `gorm:"type:timestamptz;not null;index"`
	UsedAt    *time.Time `gorm:"type:timestamptz"`
	CreatedAt time.Time  `gorm:"type:timestamptz;not null;autoCreateTime"`
}

func (ActionToken) TableName() string { return "identity.action_tokens" }

type EmailOutbox struct {
	ID            int64      `gorm:"primaryKey;autoIncrement"`
	Kind          string     `gorm:"size:32;not null;index"`
	Recipient     string     `gorm:"size:320;not null"`
	Subject       string     `gorm:"size:255;not null"`
	TokenCipher   []byte     `gorm:"type:bytea;not null"`
	Attempts      int        `gorm:"not null;default:0"`
	NextAttemptAt time.Time  `gorm:"type:timestamptz;not null;index"`
	SentAt        *time.Time `gorm:"type:timestamptz;index"`
	LastError     string     `gorm:"type:text;not null;default:''"`
	CreatedAt     time.Time  `gorm:"type:timestamptz;not null;autoCreateTime"`
}

func (EmailOutbox) TableName() string { return "identity.email_outbox" }

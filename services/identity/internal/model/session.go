package model

import "time"

type Session struct {
	ID                 string     `gorm:"primaryKey;size:64"`
	UserID             int64      `gorm:"not null;index"`
	DeviceLabel        string     `gorm:"size:120;not null;default:''"`
	RefreshDigest      []byte     `gorm:"type:bytea;not null"`
	PreviousDigest     []byte     `gorm:"type:bytea"`
	RefreshTokenCipher []byte     `gorm:"type:bytea"`
	CreatedAt          time.Time  `gorm:"type:timestamptz;not null;autoCreateTime"`
	LastSeenAt         time.Time  `gorm:"type:timestamptz;not null"`
	ExpiresAt          time.Time  `gorm:"type:timestamptz;not null;index"`
	RotatedAt          *time.Time `gorm:"type:timestamptz"`
	RevokedAt          *time.Time `gorm:"type:timestamptz;index"`
	RevokedReason      string     `gorm:"size:64;not null;default:''"`
}

func (Session) TableName() string { return "identity.sessions" }

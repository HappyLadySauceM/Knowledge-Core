package model

import "time"

const (
	// RoleColumnSize matches identity.users.role varchar width (admin/user).
	// RoleColumnSize 与 identity.users.role 的 varchar 宽度一致（admin/user）。
	RoleColumnSize = 16
	// StatusColumnSize fits pending_verification (20 runes) with headroom.
	// StatusColumnSize 需容纳 pending_verification（20 字符）并留余量。
	StatusColumnSize = 32
)

// User is the PostgreSQL persistence model. Transport and application layers
// use domain.User instead so GORM details remain inside the repository.
type User struct {
	ID                  int64      `gorm:"primaryKey;autoIncrement"`
	Username            string     `gorm:"size:32;not null"`
	Email               string     `gorm:"size:320;not null"`
	PasswordHash        string     `gorm:"type:text;not null"`
	Role                string     `gorm:"size:16;not null;default:user"`
	Status              string     `gorm:"size:32;not null;default:active"`
	TokenVersion        int64      `gorm:"not null;default:1"`
	Avatar              string     `gorm:"type:text;not null;default:''"`
	AvatarAttachmentID  *string    `gorm:"type:uuid"`
	Bio                 string     `gorm:"size:500;not null;default:''"`
	FailedLoginAttempts int        `gorm:"not null;default:0"`
	LockedUntil         *time.Time `gorm:"type:timestamptz"`
	EmailVerifiedAt     *time.Time `gorm:"type:timestamptz"`
	CreatedAt           time.Time  `gorm:"type:timestamptz;not null;autoCreateTime"`
	UpdatedAt           time.Time  `gorm:"type:timestamptz;not null;autoCreateTime;autoUpdateTime"`
}

func (User) TableName() string { return "identity.users" }

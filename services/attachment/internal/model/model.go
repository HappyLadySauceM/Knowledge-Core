package model

import "time"

type Attachment struct {
	ID             string    `gorm:"type:uuid;primaryKey"`
	OwnerID        int64     `gorm:"not null"`
	Filename       string    `gorm:"size:255;not null"`
	MediaType      string    `gorm:"size:127;not null"`
	Category       string    `gorm:"size:32;not null"`
	SizeBytes      int64     `gorm:"not null"`
	SHA256         string    `gorm:"size:64;not null"`
	DetectedType   string    `gorm:"size:127;not null;default:''"`
	ObjectKey      string    `gorm:"size:512;not null"`
	UploadID       string    `gorm:"size:256;not null"`
	Status         string    `gorm:"size:32;not null"`
	PartSize       int64     `gorm:"not null"`
	PartCount      int32     `gorm:"not null"`
	IdempotencyKey string    `gorm:"size:128;not null;default:''"`
	RequestHash    string    `gorm:"size:64;not null;default:''"`
	CreatedAt      time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt      time.Time `gorm:"type:timestamptz;not null"`
}

func (Attachment) TableName() string { return "attachment.attachments" }

type ScanJob struct {
	AttachmentID  string     `gorm:"type:uuid;primaryKey"`
	Attempts      int        `gorm:"not null;default:0"`
	NextAttemptAt time.Time  `gorm:"type:timestamptz;not null"`
	LeaseUntil    *time.Time `gorm:"type:timestamptz"`
	ParkedAt      *time.Time `gorm:"type:timestamptz"`
	LastError     string     `gorm:"size:255;not null;default:''"`
	CreatedAt     time.Time  `gorm:"type:timestamptz;not null"`
	UpdatedAt     time.Time  `gorm:"type:timestamptz;not null"`
}

func (ScanJob) TableName() string { return "attachment.scan_jobs" }

type Reference struct {
	AttachmentID string    `gorm:"type:uuid;primaryKey"`
	RefType      string    `gorm:"size:32;primaryKey"`
	RefID        string    `gorm:"size:128;primaryKey"`
	CreatedAt    time.Time `gorm:"type:timestamptz;not null"`
}

func (Reference) TableName() string { return "attachment.references" }

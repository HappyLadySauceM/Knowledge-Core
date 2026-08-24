package model

import "time"

type Configuration struct {
	Environment    string    `gorm:"size:32;primaryKey"`
	Namespace      string    `gorm:"size:32;primaryKey"`
	Revision       int64     `gorm:"not null"`
	SchemaVersion  int32     `gorm:"not null"`
	PublicValues   []byte    `gorm:"type:jsonb;not null"`
	SecretEnvelope []byte    `gorm:"type:jsonb"`
	UpdatedBy      int64     `gorm:"not null"`
	CreatedAt      time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt      time.Time `gorm:"type:timestamptz;not null"`
}

func (Configuration) TableName() string { return "platform.configurations" }

type ConfigurationRevision struct {
	Environment    string    `gorm:"size:32;primaryKey"`
	Namespace      string    `gorm:"size:32;primaryKey"`
	Revision       int64     `gorm:"primaryKey"`
	SchemaVersion  int32     `gorm:"not null"`
	PublicValues   []byte    `gorm:"type:jsonb;not null"`
	SecretEnvelope []byte    `gorm:"type:jsonb"`
	UpdatedBy      int64     `gorm:"not null"`
	CreatedAt      time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt      time.Time `gorm:"type:timestamptz;not null"`
}

func (ConfigurationRevision) TableName() string { return "platform.configuration_revisions" }

type ConfigAudit struct {
	ID             string    `gorm:"type:uuid;primaryKey"`
	Environment    string    `gorm:"size:32;not null"`
	Namespace      string    `gorm:"size:32;not null"`
	Revision       int64     `gorm:"not null"`
	ActorID        int64     `gorm:"not null"`
	PreviousDigest string    `gorm:"size:64;not null"`
	NextDigest     string    `gorm:"size:64;not null"`
	ChangedKeys    []byte    `gorm:"type:jsonb;not null"`
	CreatedAt      time.Time `gorm:"type:timestamptz;not null"`
}

func (ConfigAudit) TableName() string { return "platform.config_audit" }

type ConfigIdempotency struct {
	Environment          string    `gorm:"size:32;primaryKey"`
	ActorID              int64     `gorm:"primaryKey"`
	Namespace            string    `gorm:"size:32;primaryKey"`
	Key                  string    `gorm:"size:128;primaryKey"`
	RequestHash          string    `gorm:"size:64;not null"`
	Revision             int64     `gorm:"not null"`
	SchemaVersion        int32     `gorm:"not null"`
	ResponsePublicValues []byte    `gorm:"type:jsonb;not null"`
	ResponseSecretKeys   []byte    `gorm:"type:jsonb;not null"`
	ResponseUpdatedAt    time.Time `gorm:"type:timestamptz;not null"`
	ExpiresAt            time.Time `gorm:"type:timestamptz;not null"`
	CreatedAt            time.Time `gorm:"type:timestamptz;not null"`
}

func (ConfigIdempotency) TableName() string { return "platform.config_idempotency" }

type ConfigOutbox struct {
	ID            string     `gorm:"type:uuid;primaryKey"`
	Environment   string     `gorm:"size:32;not null"`
	Namespace     string     `gorm:"size:32;not null"`
	Revision      int64      `gorm:"not null"`
	Subject       string     `gorm:"size:128;not null"`
	Payload       []byte     `gorm:"type:jsonb;not null"`
	TraceHeaders  []byte     `gorm:"type:jsonb;not null"`
	Attempts      int        `gorm:"not null"`
	NextAttemptAt time.Time  `gorm:"type:timestamptz;not null"`
	LastErrorKey  string     `gorm:"size:64;not null"`
	ParkedAt      *time.Time `gorm:"type:timestamptz"`
	PublishedAt   *time.Time `gorm:"type:timestamptz"`
	CreatedAt     time.Time  `gorm:"type:timestamptz;not null"`
}

func (ConfigOutbox) TableName() string { return "platform.config_outbox" }

type ConfigDelivery struct {
	Environment  string     `gorm:"size:32;primaryKey"`
	Namespace    string     `gorm:"size:32;primaryKey"`
	Revision     int64      `gorm:"primaryKey"`
	Consumer     string     `gorm:"size:64;primaryKey"`
	MessageID    string     `gorm:"type:uuid;not null"`
	Status       string     `gorm:"size:32;not null"`
	Attempts     int        `gorm:"not null"`
	LastErrorKey string     `gorm:"size:128;not null"`
	AppliedAt    *time.Time `gorm:"type:timestamptz"`
	UpdatedAt    time.Time  `gorm:"type:timestamptz;not null"`
}

func (ConfigDelivery) TableName() string { return "platform.config_deliveries" }

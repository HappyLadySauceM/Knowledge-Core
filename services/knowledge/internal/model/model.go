package model

import "time"

type Document struct {
	ID                 string     `gorm:"type:uuid;primaryKey"`
	Title              string     `gorm:"size:200;not null"`
	Summary            string     `gorm:"size:1000;not null;default:''"`
	Slug               string     `gorm:"size:80;not null"`
	Language           string     `gorm:"size:16;not null;default:'zh-CN'"`
	OwnerID            int64      `gorm:"not null"`
	OwnerUsername      string     `gorm:"size:32;not null"`
	OwnerAvatar        string     `gorm:"type:text;not null;default:''"`
	Published          bool       `gorm:"not null;default:false"`
	MetadataRevision   int64      `gorm:"not null;default:1"`
	ContentRevision    int64      `gorm:"not null;default:0"`
	PermissionRevision int64      `gorm:"not null;default:1"`
	PublishedAt        *time.Time `gorm:"type:timestamptz"`
	DeletedAt          *time.Time `gorm:"type:timestamptz"`
	PurgeAfter         *time.Time `gorm:"type:timestamptz"`
	CreatedAt          time.Time  `gorm:"type:timestamptz;not null"`
	UpdatedAt          time.Time  `gorm:"type:timestamptz;not null"`
}

func (Document) TableName() string { return "knowledge.documents" }

type SlugAlias struct {
	Slug       string     `gorm:"size:80;primaryKey"`
	DocumentID *string    `gorm:"type:uuid"`
	GoneAt     *time.Time `gorm:"type:timestamptz"`
	CreatedAt  time.Time  `gorm:"type:timestamptz;not null"`
}

func (SlugAlias) TableName() string { return "knowledge.slug_aliases" }

type Member struct {
	DocumentID string    `gorm:"type:uuid;primaryKey"`
	UserID     int64     `gorm:"primaryKey"`
	Username   string    `gorm:"size:32;not null"`
	Avatar     string    `gorm:"type:text;not null;default:''"`
	Role       string    `gorm:"size:16;not null"`
	Revision   int64     `gorm:"not null;default:1"`
	CreatedBy  int64     `gorm:"not null"`
	CreatedAt  time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt  time.Time `gorm:"type:timestamptz;not null"`
}

func (Member) TableName() string { return "knowledge.document_members" }

type Projection struct {
	DocumentID  string    `gorm:"type:uuid;primaryKey"`
	Sequence    int64     `gorm:"not null"`
	Content     []byte    `gorm:"type:jsonb;not null"`
	PlainText   string    `gorm:"type:text;not null"`
	ProjectedAt time.Time `gorm:"type:timestamptz;not null"`
}

func (Projection) TableName() string { return "knowledge.document_projections" }

type Folder struct {
	ID        string    `gorm:"type:uuid;primaryKey"`
	OwnerID   int64     `gorm:"not null"`
	ParentID  *string   `gorm:"type:uuid"`
	Name      string    `gorm:"size:120;not null"`
	Depth     int       `gorm:"not null"`
	Revision  int64     `gorm:"not null;default:1"`
	CreatedAt time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt time.Time `gorm:"type:timestamptz;not null"`
}

func (Folder) TableName() string { return "knowledge.folders" }

type DocumentPlacement struct {
	OwnerID    int64     `gorm:"primaryKey"`
	DocumentID string    `gorm:"type:uuid;primaryKey"`
	FolderID   *string   `gorm:"type:uuid"`
	Revision   int64     `gorm:"not null;default:1"`
	CreatedAt  time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt  time.Time `gorm:"type:timestamptz;not null"`
}

func (DocumentPlacement) TableName() string { return "knowledge.document_placements" }

type Tag struct {
	ID        string    `gorm:"type:uuid;primaryKey"`
	OwnerID   int64     `gorm:"not null"`
	Name      string    `gorm:"size:64;not null"`
	Slug      string    `gorm:"size:80;not null"`
	CreatedAt time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt time.Time `gorm:"type:timestamptz;not null"`
}

func (Tag) TableName() string { return "knowledge.tags" }

type DocumentTag struct {
	DocumentID string    `gorm:"type:uuid;primaryKey"`
	TagID      string    `gorm:"type:uuid;primaryKey"`
	CreatedAt  time.Time `gorm:"type:timestamptz;not null"`
}

func (DocumentTag) TableName() string { return "knowledge.document_tags" }

type DocumentPublication struct {
	DocumentID      string    `gorm:"type:uuid;primaryKey"`
	VersionID       *string   `gorm:"type:uuid"`
	VersionSequence int64     `gorm:"not null"`
	Title           string    `gorm:"size:200;not null"`
	Summary         string    `gorm:"size:1000;not null;default:''"`
	Slug            string    `gorm:"size:80;not null"`
	Language        string    `gorm:"size:16;not null;default:'zh-CN'"`
	Tags            []byte    `gorm:"type:jsonb;not null"`
	OwnerID         int64     `gorm:"not null"`
	OwnerUsername   string    `gorm:"size:32;not null"`
	OwnerAvatar     string    `gorm:"type:text;not null;default:''"`
	Content         []byte    `gorm:"type:jsonb;not null"`
	PlainText       string    `gorm:"type:text;not null;default:''"`
	PublishedAt     time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt       time.Time `gorm:"type:timestamptz;not null"`
}

func (DocumentPublication) TableName() string { return "knowledge.document_publications" }

type PublicationAttachment struct {
	DocumentID   string `gorm:"type:uuid;primaryKey"`
	AttachmentID string `gorm:"type:uuid;primaryKey"`
}

func (PublicationAttachment) TableName() string { return "knowledge.publication_attachments" }

type Attachment struct {
	ID            string    `gorm:"type:uuid;primaryKey"`
	DocumentID    string    `gorm:"type:uuid;not null"`
	UploaderID    int64     `gorm:"not null"`
	Filename      string    `gorm:"size:255;not null"`
	DeclaredType  string    `gorm:"size:127;not null"`
	DetectedType  string    `gorm:"size:127;not null;default:''"`
	SizeBytes     int64     `gorm:"not null"`
	SHA256        string    `gorm:"size:64;not null"`
	ObjectKey     string    `gorm:"size:512;not null"`
	Status        string    `gorm:"size:32;not null"`
	FailureReason string    `gorm:"size:64;not null;default:''"`
	UploadExpires time.Time `gorm:"type:timestamptz;not null"`
	CreatedAt     time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt     time.Time `gorm:"type:timestamptz;not null"`
}

func (Attachment) TableName() string { return "knowledge.attachments" }

type AttachmentScanJob struct {
	AttachmentID  string    `gorm:"type:uuid;primaryKey"`
	Attempts      int       `gorm:"not null;default:0"`
	NextAttemptAt time.Time `gorm:"type:timestamptz;not null"`
	LastErrorKey  string    `gorm:"size:64;not null;default:''"`
	CreatedAt     time.Time `gorm:"type:timestamptz;not null"`
	UpdatedAt     time.Time `gorm:"type:timestamptz;not null"`
}

func (AttachmentScanJob) TableName() string { return "knowledge.attachment_scan_jobs" }

type Outbox struct {
	ID            string     `gorm:"type:uuid;primaryKey"`
	Subject       string     `gorm:"size:128;not null"`
	Payload       []byte     `gorm:"type:jsonb;not null"`
	TraceHeaders  []byte     `gorm:"type:jsonb;not null;default:'{}'"`
	Attempts      int        `gorm:"not null;default:0"`
	NextAttemptAt time.Time  `gorm:"type:timestamptz;not null"`
	LastErrorKey  string     `gorm:"size:64;not null;default:''"`
	ParkedAt      *time.Time `gorm:"type:timestamptz"`
	PublishedAt   *time.Time `gorm:"type:timestamptz"`
	CreatedAt     time.Time  `gorm:"type:timestamptz;not null"`
}

func (Outbox) TableName() string { return "knowledge.outbox" }

type IdempotencyKey struct {
	ActorID     int64     `gorm:"primaryKey"`
	Operation   string    `gorm:"size:64;primaryKey"`
	Key         string    `gorm:"size:128;primaryKey"`
	ResourceID  string    `gorm:"type:uuid;not null"`
	RequestHash string    `gorm:"size:64;not null"`
	ExpiresAt   time.Time `gorm:"type:timestamptz;not null"`
	CreatedAt   time.Time `gorm:"type:timestamptz;not null"`
}

func (IdempotencyKey) TableName() string { return "knowledge.idempotency_keys" }

package assets

import "time"

const (
	StatusReady   = "ready"
	StatusDeleted = "deleted"
)

// Asset is metadata for a locally stored upload.
// Asset 是本地上传文件的元数据。
type Asset struct {
	ID           int64
	StorageKey   string
	OriginalName string
	ContentType  string
	SizeBytes    int64
	SHA256       string
	Status       string
	CreatedBy    int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

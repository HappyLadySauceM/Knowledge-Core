package options

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
)

const (
	defaultUploadDir       = ".uploads"
	defaultUploadMaxBytes  = int64(10 * 1024 * 1024)
	defaultUploadPublicURL = "/api/v1/assets"
)

// UploadOptions controls local asset storage.
// UploadOptions 控制本地资源存储。
type UploadOptions struct {
	Dir        string `json:"dir" mapstructure:"dir"`
	MaxBytes   int64  `json:"max-bytes" mapstructure:"max-bytes"`
	PublicPath string `json:"public-path" mapstructure:"public-path"`
}

// NewUploadOptions returns local upload defaults.
// NewUploadOptions 返回本地上传默认配置。
func NewUploadOptions() *UploadOptions {
	return &UploadOptions{
		Dir:        defaultUploadDir,
		MaxBytes:   defaultUploadMaxBytes,
		PublicPath: defaultUploadPublicURL,
	}
}

// Validate normalizes and validates local upload settings.
// Validate 规范化并校验本地上传配置。
func (u *UploadOptions) Validate() error {
	if u == nil {
		return fmt.Errorf("uploads config is nil")
	}
	u.Dir = strings.TrimSpace(u.Dir)
	if u.Dir == "" {
		u.Dir = defaultUploadDir
	}
	u.Dir = filepath.Clean(u.Dir)
	if u.MaxBytes <= 0 {
		u.MaxBytes = defaultUploadMaxBytes
	}
	u.PublicPath = strings.TrimSpace(u.PublicPath)
	if u.PublicPath == "" {
		u.PublicPath = defaultUploadPublicURL
	}
	if !strings.HasPrefix(u.PublicPath, "/") || strings.HasSuffix(u.PublicPath, "/") {
		return fmt.Errorf("uploads public-path must be an absolute URL path without a trailing slash")
	}
	return nil
}

// AddFlags registers local upload settings.
// AddFlags 注册本地上传配置标志。
func (u *UploadOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&u.Dir, "upload-dir", defaultUploadDir, "Local upload directory")
	fs.Int64Var(&u.MaxBytes, "upload-max-bytes", defaultUploadMaxBytes, "Maximum uploaded file size in bytes")
	fs.StringVar(&u.PublicPath, "upload-public-path", defaultUploadPublicURL, "Public URL path prefix for uploaded assets")
}

package assets

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apperrors "github.com/HappyLadySauce/Knowledge-Core/internal/errors"
	"github.com/HappyLadySauce/Knowledge-Core/internal/options"
	"github.com/HappyLadySauce/Knowledge-Core/internal/user"
	"github.com/HappyLadySauce/Knowledge-Core/pkg/localstorage"
)

type storage interface {
	Put(context.Context, string, io.Reader, int64) (localstorage.StoredFile, error)
	Open(string) (localstorage.ReadSeekCloser, error)
}

// Service implements local asset upload and retrieval use cases.
// Service 实现本地资源上传与读取用例。
type Service struct {
	repo       *Repository
	store      storage
	maxBytes   int64
	publicPath string
}

// NewService creates an asset service backed by local storage.
// NewService 创建使用本地存储的资源服务。
func NewService(db *sql.DB, store storage, cfg *options.UploadOptions) *Service {
	if cfg == nil {
		cfg = options.NewUploadOptions()
	}
	return &Service{
		repo:       NewRepository(db),
		store:      store,
		maxBytes:   cfg.MaxBytes,
		publicPath: cfg.PublicPath,
	}
}

// UploadInput contains an uploaded file stream and its client metadata.
// UploadInput 包含上传文件流及客户端元数据。
type UploadInput struct {
	Filename string
	Size     int64
	Body     io.Reader
}

// Upload stores an image for an administrator.
// Upload 为管理员保存图片。
func (s *Service) Upload(ctx context.Context, actor user.User, input UploadInput) (Asset, error) {
	if actor.Role != user.RoleAdmin {
		return Asset{}, apperrors.Forbidden
	}
	if input.Body == nil || input.Size < 0 {
		return Asset{}, apperrors.InvalidRequest
	}
	if input.Size > s.maxBytes {
		return Asset{}, apperrors.InvalidRequest
	}
	filename := safeFilename(input.Filename)
	head := make([]byte, 512)
	n, err := io.ReadFull(input.Body, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Asset{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	if n == 0 {
		return Asset{}, apperrors.InvalidRequest
	}
	contentType, extension, ok := detectImageType(head[:n])
	if !ok {
		return Asset{}, apperrors.InvalidRequest
	}
	key, err := newStorageKey(extension)
	if err != nil {
		return Asset{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	stored, err := s.store.Put(ctx, key, io.MultiReader(bytes.NewReader(head[:n]), input.Body), s.maxBytes)
	if err != nil {
		if errors.Is(err, localstorage.ErrTooLarge) {
			return Asset{}, apperrors.InvalidRequest
		}
		return Asset{}, apperrors.Wrap(apperrors.InternalError, err)
	}
	now := time.Now().UTC()
	created, err := s.repo.Create(ctx, Asset{
		StorageKey:   stored.Key,
		OriginalName: filename,
		ContentType:  contentType,
		SizeBytes:    stored.Size,
		SHA256:       stored.SHA256,
		Status:       StatusReady,
		CreatedBy:    actor.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		_ = s.deleteStoredFile(stored.Key)
		return Asset{}, err
	}
	return created, nil
}

// OpenPublic opens a ready asset for public HTTP serving.
// OpenPublic 打开可公开访问的资源供 HTTP 输出。
func (s *Service) OpenPublic(ctx context.Context, id int64) (Asset, localstorage.ReadSeekCloser, error) {
	if id <= 0 {
		return Asset{}, nil, apperrors.InvalidRequest
	}
	asset, err := s.repo.GetReadyByID(ctx, id)
	if err != nil {
		return Asset{}, nil, err
	}
	file, err := s.store.Open(asset.StorageKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Asset{}, nil, apperrors.NotFound
		}
		return Asset{}, nil, apperrors.Wrap(apperrors.InternalError, err)
	}
	return asset, file, nil
}

// Delete marks an asset deleted without removing the file immediately.
// Delete 将资源标记为删除，但不立即移除文件。
func (s *Service) Delete(ctx context.Context, actor user.User, id int64) error {
	if actor.Role != user.RoleAdmin {
		return apperrors.Forbidden
	}
	if id <= 0 {
		return apperrors.InvalidRequest
	}
	return s.repo.MarkDeleted(ctx, id, time.Now().UTC())
}

// PublicURL returns the stable API URL for an asset.
// PublicURL 返回资源稳定的 API URL。
func (s *Service) PublicURL(asset Asset) string {
	return strings.TrimRight(s.publicPath, "/") + "/" + strconv.FormatInt(asset.ID, 10) + "/content"
}

func (s *Service) deleteStoredFile(key string) error {
	if remover, ok := s.store.(interface{ Delete(string) error }); ok {
		return remover.Delete(key)
	}
	return nil
}

func detectImageType(header []byte) (string, string, bool) {
	if len(header) >= 12 && string(header[4:8]) == "ftyp" {
		brand := string(header[8:12])
		if brand == "avif" || brand == "avis" {
			return "image/avif", ".avif", true
		}
	}
	contentType := http.DetectContentType(header)
	extensions := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
		"image/avif": ".avif",
	}
	extension, ok := extensions[contentType]
	if !ok {
		return "", "", false
	}
	return contentType, extension, ok
}

func newStorageKey(extension string) (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	return path.Join("images", now.Format("2006"), now.Format("01"), hex.EncodeToString(random[:])+extension), nil
}

func safeFilename(filename string) string {
	filename = strings.TrimSpace(strings.ReplaceAll(filename, "\\", "/"))
	filename = path.Base(filename)
	if filename == "." || filename == "/" || filename == "" {
		return "upload"
	}
	var cleaned strings.Builder
	for _, r := range filename {
		if r < 0x20 || r == 0x7f {
			continue
		}
		cleaned.WriteRune(r)
	}
	filename = strings.TrimSpace(cleaned.String())
	if filename == "" {
		return "upload"
	}
	if len(filename) > 255 {
		for len(filename) > 255 {
			_, size := utf8.DecodeLastRuneInString(filename)
			filename = filename[:len(filename)-size]
		}
	}
	return filename
}

package localstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidKey = errors.New("invalid storage key")
	ErrTooLarge   = errors.New("stored file exceeds maximum size")
)

// ReadSeekCloser is the file handle needed by HTTP range serving.
// ReadSeekCloser 是 HTTP 范围响应所需的文件句柄接口。
type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

// StoredFile describes the bytes committed to local storage.
// StoredFile 描述已提交到本地存储的文件。
type StoredFile struct {
	Key    string
	Size   int64
	SHA256 string
}

// FileStore stores files below one configured root directory.
// FileStore 将文件存储在一个配置的根目录下。
type FileStore struct {
	root string
}

// New creates a local file store and its temporary directory.
// New 创建本地文件存储及临时目录。
func New(root string) (*FileStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrInvalidKey
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, ".tmp"), 0o750); err != nil {
		return nil, err
	}
	return &FileStore{root: absolute}, nil
}

// Put writes a file atomically and returns its checksum.
// Put 原子写入文件并返回校验和。
func (s *FileStore) Put(ctx context.Context, key string, source io.Reader, maxBytes int64) (StoredFile, error) {
	if maxBytes <= 0 {
		return StoredFile{}, ErrTooLarge
	}
	finalPath, err := s.resolve(key)
	if err != nil {
		return StoredFile{}, err
	}
	if source == nil {
		return StoredFile{}, io.ErrUnexpectedEOF
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o750); err != nil {
		return StoredFile{}, err
	}
	temp, err := os.CreateTemp(filepath.Join(s.root, ".tmp"), "upload-*")
	if err != nil {
		return StoredFile{}, err
	}
	tempPath := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(&contextReader{ctx: ctx, reader: source}, maxBytes+1)
	size, err := io.Copy(io.MultiWriter(temp, hasher), limited)
	if err != nil {
		return StoredFile{}, err
	}
	if size > maxBytes {
		return StoredFile{}, ErrTooLarge
	}
	if err := temp.Sync(); err != nil {
		return StoredFile{}, err
	}
	if err := temp.Close(); err != nil {
		return StoredFile{}, err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return StoredFile{}, err
	}
	keep = true
	return StoredFile{Key: key, Size: size, SHA256: hex.EncodeToString(hasher.Sum(nil))}, nil
}

// Open opens a stored file after validating its relative key.
// Open 校验相对 key 后打开已存储文件。
func (s *FileStore) Open(key string) (ReadSeekCloser, error) {
	filePath, err := s.resolve(key)
	if err != nil {
		return nil, err
	}
	return os.Open(filePath)
}

// Delete removes a stored file. Missing files are treated as already deleted.
// Delete 删除已存储文件；文件不存在时视为已删除。
func (s *FileStore) Delete(key string) error {
	filePath, err := s.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *FileStore) resolve(key string) (string, error) {
	key = strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	if key == "" || strings.ContainsRune(key, '\x00') || strings.Contains(key, ":") {
		return "", ErrInvalidKey
	}
	clean := path.Clean(key)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, ".tmp/") {
		return "", ErrInvalidKey
	}
	full := filepath.Join(s.root, filepath.FromSlash(clean))
	relative, err := filepath.Rel(s.root, full)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	return full, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

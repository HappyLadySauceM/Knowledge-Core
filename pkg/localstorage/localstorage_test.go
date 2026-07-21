package localstorage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStorePutOpenAndDelete(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	want := []byte("image bytes")
	stored, err := store.Put(context.Background(), "images/2026/07/test.bin", bytes.NewReader(want), int64(len(want)))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if stored.Size != int64(len(want)) || stored.SHA256 == "" {
		t.Fatalf("stored metadata = %+v", stored)
	}

	file, err := store.Open(stored.Key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	got, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}

	if err := store.Delete(stored.Key); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Open(stored.Key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open() after Delete() error = %v, want not-exist", err)
	}
}

func TestFileStoreRejectsTraversalAndOversizedWrites(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := store.Put(context.Background(), "../outside", bytes.NewReader([]byte("x")), 10); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Put() traversal error = %v, want ErrInvalidKey", err)
	}
	if _, err := store.Put(context.Background(), "images/too-large", bytes.NewReader([]byte("1234")), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put() oversized error = %v, want ErrTooLarge", err)
	}
	if _, err := os.Stat(filepath.Join(root, "images", "too-large")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized file exists: %v", err)
	}
}

func TestFileStoreHonorsCanceledContext(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.Put(ctx, "images/canceled", bytes.NewReader([]byte("x")), 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put() canceled error = %v, want context.Canceled", err)
	}
}

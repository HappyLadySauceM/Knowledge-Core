package service

import (
	"context"
	"io"
	"strings"
	"testing"

	coretrace "github.com/HappyLadySauce/Knowledge-Core/pkg/trace"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/scanner"
	"gorm.io/gorm"
)

type scanStoreStub struct {
	row         *domain.Attachment
	err         error
	claimCtx    context.Context
	completeCtx context.Context
	completed   bool
}

func (s *scanStoreStub) Claim(ctx context.Context) (*domain.Attachment, error) {
	s.claimCtx = ctx
	if s.err != nil {
		return nil, s.err
	}
	return s.row, nil
}

func (s *scanStoreStub) RetryScan(context.Context, string, error) error { return nil }

func (s *scanStoreStub) CompleteScan(ctx context.Context, _ string, _ domain.ScanResult, _ bool) error {
	s.completeCtx = ctx
	s.completed = true
	return nil
}

type objectStub struct {
	ctx context.Context
}

func (s *objectStub) OpenObject(ctx context.Context, _ string) (io.ReadCloser, error) {
	s.ctx = ctx
	return io.NopCloser(strings.NewReader("payload")), nil
}

type scannerStub struct {
	ctx    context.Context
	result scanner.Result
}

func (s *scannerStub) Scan(ctx context.Context, _ io.Reader) (scanner.Result, error) {
	s.ctx = ctx
	return s.result, nil
}

func TestScanOnceSuppressesEmptyClaim(t *testing.T) {
	t.Parallel()

	store := &scanStoreStub{err: gorm.ErrRecordNotFound}
	if err := scanOnce(context.Background(), store, &objectStub{}, &scannerStub{}); err != nil {
		t.Fatalf("scanOnce() error = %v", err)
	}
	if store.claimCtx == nil {
		t.Fatal("Claim() was not called")
	}
	if !coretrace.IsSuppressed(store.claimCtx) {
		t.Fatal("Claim() context should be suppressed for empty-poll noise control")
	}
}

func TestScanOnceUsesWorkContextAfterClaim(t *testing.T) {
	t.Parallel()

	store := &scanStoreStub{row: &domain.Attachment{
		ID: "attachment-1", ObjectKey: "objects/attachment-1", Status: domain.StatusScanning, SizeBytes: 7,
	}}
	objects := &objectStub{}
	content := &scannerStub{result: scanner.Result{Clean: true, SHA256: strings.Repeat("ab", 32), Size: 7, DetectedType: "text/plain"}}

	if err := scanOnce(context.Background(), store, objects, content); err != nil {
		t.Fatalf("scanOnce() error = %v", err)
	}
	if !coretrace.IsSuppressed(store.claimCtx) {
		t.Fatal("Claim() context should stay suppressed")
	}
	if objects.ctx == nil || content.ctx == nil || store.completeCtx == nil {
		t.Fatal("scan work dependencies were not called")
	}
	if coretrace.IsSuppressed(objects.ctx) || coretrace.IsSuppressed(content.ctx) || coretrace.IsSuppressed(store.completeCtx) {
		t.Fatal("claimed scan work must not inherit claim suppression")
	}
	if !store.completed {
		t.Fatal("CompleteScan() was not called")
	}
}

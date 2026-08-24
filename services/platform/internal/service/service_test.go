package service

import (
	"context"
	"testing"
	"time"

	platformv1 "github.com/HappyLadySauce/Knowledge-Core/kitex_gen/platform"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/domain"
	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/repository"
)

type fakeStore struct {
	snapshot domain.Snapshot
	put      repository.PutRequest
}

func (f *fakeStore) Get(context.Context, string) (domain.Snapshot, error) { return f.snapshot, nil }
func (f *fakeStore) GetRevision(context.Context, string, int64) (domain.Snapshot, error) {
	return f.snapshot, nil
}
func (f *fakeStore) Put(_ context.Context, request repository.PutRequest) (domain.Snapshot, error) {
	f.put = request
	return f.snapshot, nil
}
func (f *fakeStore) GetDelivery(context.Context, string, int64) (repository.Delivery, error) {
	return repository.Delivery{}, nil
}
func (f *fakeStore) ConsumerState(context.Context, string, string) (domain.ConsumerState, error) {
	return domain.ConsumerState{Environment: f.snapshot.Environment, Namespace: f.snapshot.Namespace, Status: "pending"}, nil
}
func (f *fakeStore) ReportDelivery(context.Context, domain.DeliveryUpdate) error { return nil }

func TestGetRedactsSecrets(t *testing.T) {
	t.Parallel()

	store := &fakeStore{snapshot: domain.Snapshot{
		Environment: "dev", Namespace: "ai", Revision: 3, SchemaVersion: 1,
		Public: map[string]string{"enabled": "true"}, Secrets: map[string]string{"api_key": "must-not-leak"},
		UpdatedAt: time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC), UpdatedBy: 42,
	}}
	service, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	configuration, err := service.Get(context.Background(), "ai")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, value := range configuration.Values {
		if value.Key == "api_key" {
			if value.Value != "" || !value.Secret || !value.Redacted {
				t.Fatalf("api_key = %#v, want configured redaction", value)
			}
			return
		}
	}
	t.Fatal("redacted api_key entry was not returned")
}

func TestPutBuildsStableRequestHash(t *testing.T) {
	t.Parallel()

	store := &fakeStore{snapshot: domain.Snapshot{Environment: "dev", Namespace: "site", Public: map[string]string{}, Secrets: map[string]string{}}}
	service, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := &platformv1.PutConfigurationRequest{Namespace: "site", ExpectedRevision: 0, IdempotencyKey: "request-1", Values: map[string]string{"title": "Site"}}
	if _, err := service.Put(context.Background(), 42, request); err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	firstHash := store.put.RequestHash
	if firstHash == "" {
		t.Fatal("Put() request hash is empty")
	}
	if _, err := service.Put(context.Background(), 42, request); err != nil {
		t.Fatalf("Put(replay) error = %v", err)
	}
	if store.put.RequestHash != firstHash {
		t.Fatalf("request hash = %q, want %q", store.put.RequestHash, firstHash)
	}
}

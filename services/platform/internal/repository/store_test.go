package repository

import (
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/platform/internal/model"
)

func TestSnapshotFromIdempotencyRestoresOriginalRedactedResponse(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	snapshot, err := snapshotFromIdempotency(model.ConfigIdempotency{
		Environment: "dev", Namespace: "ai", ActorID: 42, Revision: 7, SchemaVersion: 1,
		ResponsePublicValues: []byte(`{"enabled":"true","model":"test"}`),
		ResponseSecretKeys:   []byte(`["api_key"]`), ResponseUpdatedAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("snapshotFromIdempotency() error = %v", err)
	}
	if snapshot.Revision != 7 || snapshot.UpdatedBy != 42 || snapshot.Public["model"] != "test" || snapshot.Secrets["api_key"] != "configured" || !snapshot.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("snapshotFromIdempotency() = %#v", snapshot)
	}
}

func TestSnapshotFromIdempotencyRejectsCorruptResponse(t *testing.T) {
	t.Parallel()

	if _, err := snapshotFromIdempotency(model.ConfigIdempotency{ResponsePublicValues: []byte(`{`), ResponseSecretKeys: []byte(`[]`)}); err == nil {
		t.Fatal("snapshotFromIdempotency() accepted corrupt public values")
	}
	if _, err := snapshotFromIdempotency(model.ConfigIdempotency{ResponsePublicValues: []byte(`{}`), ResponseSecretKeys: []byte(`{}`)}); err == nil {
		t.Fatal("snapshotFromIdempotency() accepted corrupt secret keys")
	}
}

func TestAcceptDeliveryUpdateDoesNotRegressTerminalState(t *testing.T) {
	t.Parallel()
	for _, terminal := range []string{"applied", "rejected", "parked"} {
		if acceptDeliveryUpdate(terminal, "retrying") {
			t.Fatalf("terminal state %q accepted a retrying downgrade", terminal)
		}
		if !acceptDeliveryUpdate(terminal, terminal) {
			t.Fatalf("terminal state %q rejected an idempotent update", terminal)
		}
	}
	if !acceptDeliveryUpdate("validating", "applied") {
		t.Fatal("validating state rejected an applied transition")
	}
}

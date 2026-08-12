package repository

import (
	"testing"
)

func TestNotifyWorkersRejectsUnknownPayload(t *testing.T) {
	t.Parallel()
	if err := notifyWorkers(nil, WorkerWakePayloadOutbox); err == nil {
		t.Fatal("notifyWorkers() error = nil, want transaction required")
	}
	if err := notifyWorkers(nil, "purge"); err == nil {
		t.Fatal("notifyWorkers() error = nil, want unsupported payload")
	}
}

func TestWorkerWakeConstantsStayLowCardinality(t *testing.T) {
	t.Parallel()
	if WorkerWakeChannel != "knowledge_workers" {
		t.Fatalf("WorkerWakeChannel = %q", WorkerWakeChannel)
	}
	if WorkerWakePayloadOutbox != "outbox" || WorkerWakePayloadAttachment != "attachment" {
		t.Fatalf("wake payloads = %q / %q", WorkerWakePayloadOutbox, WorkerWakePayloadAttachment)
	}
}

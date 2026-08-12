package repository

import (
	"fmt"

	"gorm.io/gorm"
)

// Worker wake channel and payloads are fixed, low-cardinality hints.
// Worker 唤醒 channel 与 payload 为固定低基数 hint。
const (
	WorkerWakeChannel           = "knowledge_workers"
	WorkerWakePayloadOutbox     = "outbox"
	WorkerWakePayloadAttachment = "attachment"
)

// notifyWorkers emits a same-transaction PostgreSQL NOTIFY for worker wake.
// notifyWorkers 在同一事务内发出 PostgreSQL NOTIFY，用于唤醒 worker。
func notifyWorkers(tx *gorm.DB, payload string) error {
	if tx == nil {
		return fmt.Errorf("notify knowledge workers: transaction is required")
	}
	switch payload {
	case WorkerWakePayloadOutbox, WorkerWakePayloadAttachment:
	default:
		return fmt.Errorf("notify knowledge workers: unsupported payload %q", payload)
	}
	if err := tx.Exec("SELECT pg_notify(?, ?)", WorkerWakeChannel, payload).Error; err != nil {
		return fmt.Errorf("notify knowledge workers: %w", err)
	}
	return nil
}

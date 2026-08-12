package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/repository"
	"github.com/jackc/pgx/v5/stdlib"
)

// WakeKind identifies which worker lanes a NOTIFY or reconnect should run.
// WakeKind 标识 NOTIFY 或重连应触发的 worker 处理通道。
type WakeKind uint8

const (
	WakeOutbox WakeKind = 1 << iota
	WakeAttachment
	WakeAll
)

func parseWakePayload(payload string) WakeKind {
	switch payload {
	case repository.WorkerWakePayloadOutbox:
		return WakeOutbox
	case repository.WorkerWakePayloadAttachment:
		return WakeAttachment
	default:
		return 0
	}
}

// WakeSource delivers coalescable wake hints until ctx is cancelled.
// WakeSource 在 ctx 取消前投递可合并的唤醒 hint。
type WakeSource interface {
	Run(ctx context.Context, emit func(WakeKind)) error
}

type postgresWakeSource struct {
	db     *sql.DB
	logger *slog.Logger
}

func newPostgresWakeSource(db *sql.DB, logger *slog.Logger) (*postgresWakeSource, error) {
	if db == nil || logger == nil {
		return nil, errors.New("create knowledge worker wake source: database and logger are required")
	}
	return &postgresWakeSource{db: db, logger: logger}, nil
}

func (s *postgresWakeSource) Run(ctx context.Context, emit func(WakeKind)) error {
	if s == nil {
		return errors.New("run knowledge worker wake source: source is nil")
	}
	if emit == nil {
		return errors.New("run knowledge worker wake source: emit callback is required")
	}
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		err := s.listenOnce(ctx, emit)
		if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if ctx.Err() != nil {
				return nil
			}
		}
		s.logger.WarnContext(ctx, "knowledge worker LISTEN disconnected; reconnecting",
			slog.String("component", "knowledge.worker"),
			slog.String("event", "listen_reconnect"),
			slog.String("error.type", fmt.Sprintf("%T", err)),
			slog.Duration("backoff", backoff),
		)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
		// Reconnect recovery runs a full fallback sweep.
		// 重连恢复后执行完整兜底扫描。
		emit(WakeAll)
	}
}

func (s *postgresWakeSource) listenOnce(ctx context.Context, emit func(WakeKind)) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire knowledge worker listen connection: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	return conn.Raw(func(driverConn any) error {
		stdConn, ok := driverConn.(*stdlib.Conn)
		if !ok || stdConn == nil {
			return fmt.Errorf("knowledge worker listen connection has unexpected driver type %T", driverConn)
		}
		pgConn := stdConn.Conn()
		if pgConn == nil {
			return errors.New("knowledge worker listen connection is closed")
		}
		if _, err := pgConn.Exec(ctx, "LISTEN "+repository.WorkerWakeChannel); err != nil {
			return fmt.Errorf("LISTEN %s: %w", repository.WorkerWakeChannel, err)
		}
		for {
			notification, err := pgConn.WaitForNotification(ctx)
			if err != nil {
				return err
			}
			if notification == nil {
				continue
			}
			kind := parseWakePayload(notification.Payload)
			if kind == 0 {
				continue
			}
			emit(kind)
		}
	})
}

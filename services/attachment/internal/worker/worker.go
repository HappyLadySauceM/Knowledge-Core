package worker

import (
	"context"
	"errors"
	"github.com/HappyLadySauce/Knowledge-Core/services/attachment/internal/service"
	"log/slog"
	"time"
)

type Worker struct {
	svc               *service.Service
	interval, timeout time.Duration
	ctx               context.Context
	cancel            context.CancelFunc
	logger            *slog.Logger
}

func New(svc *service.Service, interval, timeout time.Duration, logger *slog.Logger) (*Worker, error) {
	if svc == nil || interval <= 0 || timeout <= 0 || logger == nil {
		return nil, errors.New("attachment worker dependencies are required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker{svc: svc, interval: interval, timeout: timeout, ctx: ctx, cancel: cancel, logger: logger}, nil
}
func (w *Worker) Name() string { return "attachment-scan-worker" }
func (w *Worker) Serve() error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.scan(); err != nil {
			w.logger.Warn("attachment scan job failed", slog.Any("error", err))
		}
		select {
		case <-w.ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (w *Worker) scan() error {
	ctx, cancel := context.WithTimeout(w.ctx, w.timeout)
	defer cancel()
	return w.svc.ScanOnce(ctx)
}
func (w *Worker) Ready(context.Context) error { return nil }
func (w *Worker) Shutdown(context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}

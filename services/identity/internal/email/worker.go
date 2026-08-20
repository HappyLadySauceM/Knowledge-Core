package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
)

type Worker struct {
	cfg    config.SMTPOptions
	outbox repository.EmailOutboxRepository
	key    string
	logger *slog.Logger
	stop   context.CancelFunc
	done   chan struct{}
}

func NewWorker(cfg config.SMTPOptions, outbox repository.EmailOutboxRepository, key string, logger *slog.Logger) (*Worker, error) {
	if cfg.Host == "" {
		return nil, nil
	}
	if outbox == nil || len(key) < 16 || logger == nil {
		return nil, errors.New("create identity email worker: dependencies are required")
	}
	return &Worker{cfg: cfg, outbox: outbox, key: key, logger: logger, done: make(chan struct{})}, nil
}
func (w *Worker) Name() string                { return "identity-email" }
func (w *Worker) Ready(context.Context) error { return nil }
func (w *Worker) Serve() error {
	ctx, cancel := context.WithCancel(context.Background())
	w.stop = cancel
	defer close(w.done)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			w.process(ctx, now)
		}
	}
}
func (w *Worker) Shutdown(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if w.stop != nil {
		w.stop()
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (w *Worker) process(ctx context.Context, now time.Time) {
	record, err := w.outbox.Claim(ctx, now)
	if errors.Is(err, repository.ErrOutboxEmpty) {
		return
	}
	if err != nil {
		w.logger.ErrorContext(ctx, "claim identity email failed", slog.Any("error", err))
		return
	}
	token, err := security.OpenEmailToken(record.TokenCipher, w.key)
	if err == nil {
		err = w.send(record, token)
	}
	if err == nil {
		_ = w.outbox.MarkSent(ctx, record.ID, now)
		return
	}
	_ = w.outbox.MarkFailed(ctx, record.ID, err.Error(), now, backoff(record.Attempts))
}
func (w *Worker) send(record *model.EmailOutbox, token string) error {
	var link string
	if record.Kind == "email_verification" {
		link = "verify-email?token=" + token
	} else {
		link = "reset-password?token=" + token
	}
	if base := strings.TrimRight(w.cfg.FrontendBaseURL, "/"); base != "" {
		if record.Kind == "email_verification" {
			link = base + "/verify-email?token=" + token
		} else {
			link = base + "/reset-password?token=" + token
		}
	}
	body := fmt.Sprintf("To continue, open this link: %s\n", link)
	message := "From: " + w.cfg.From + "\r\nTo: " + record.Recipient + "\r\nSubject: " + record.Subject + "\r\n\r\n" + body
	address := w.cfg.Host + ":" + strconv.Itoa(w.cfg.Port)
	var auth smtp.Auth
	if w.cfg.Username != "" {
		auth = smtp.PlainAuth("", w.cfg.Username, w.cfg.Password, w.cfg.Host)
	}
	return smtp.SendMail(address, auth, w.cfg.From, []string{record.Recipient}, []byte(message))
}
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		attempts = 8
	}
	return time.Duration(1<<attempts) * time.Minute
}

var _ coreapp.Component = (*Worker)(nil)

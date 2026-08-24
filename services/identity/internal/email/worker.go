package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	coreapp "github.com/HappyLadySauce/Knowledge-Core/pkg/app"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/config"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/model"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/repository"
	"github.com/HappyLadySauce/Knowledge-Core/services/identity/internal/security"
)

type Worker struct {
	cfg    atomic.Pointer[config.SMTPOptions]
	outbox repository.EmailOutboxRepository
	key    string
	logger *slog.Logger
	stop   context.CancelFunc
	done   chan struct{}
}

func NewWorker(cfg config.SMTPOptions, outbox repository.EmailOutboxRepository, key string, logger *slog.Logger) (*Worker, error) {
	if outbox == nil || len(key) < 16 || logger == nil {
		return nil, errors.New("create identity email worker: dependencies are required")
	}
	worker := &Worker{outbox: outbox, key: key, logger: logger, done: make(chan struct{})}
	worker.cfg.Store(&cfg)
	return worker, nil
}

func (w *Worker) ApplyConfig(ctx context.Context, cfg config.SMTPOptions) error {
	if w == nil {
		return errors.New("apply identity email configuration: worker is required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate identity email configuration: %w", err)
	}
	if err := Probe(ctx, cfg); err != nil {
		return err
	}
	candidate := cfg
	w.cfg.Store(&candidate)
	return nil
}

func (w *Worker) CurrentConfig() config.SMTPOptions {
	if w == nil {
		return config.SMTPOptions{}
	}
	value := w.cfg.Load()
	if value == nil {
		return config.SMTPOptions{}
	}
	return *value
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
	cfg := w.CurrentConfig()
	if cfg.Host == "" {
		return
	}
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
		err = w.send(cfg, record, token)
	}
	if err == nil {
		if markErr := w.outbox.MarkSent(ctx, record.ID, now); markErr != nil {
			w.logger.ErrorContext(ctx, "mark identity email sent failed", slog.String("message_id", record.MessageID), slog.Any("error", markErr))
		}
		return
	}
	if markErr := w.outbox.MarkFailed(ctx, record.ID, err.Error(), now, backoff(record.Attempts)); markErr != nil {
		w.logger.ErrorContext(ctx, "mark identity email failed", slog.String("message_id", record.MessageID), slog.Any("error", markErr))
	}
}
func (w *Worker) send(cfg config.SMTPOptions, record *model.EmailOutbox, token string) error {
	var link string
	if record.Kind == "email_verification" {
		link = "verify-email?token=" + token
	} else {
		link = "reset-password?token=" + token
	}
	if base := strings.TrimRight(cfg.FrontendBaseURL, "/"); base != "" {
		if record.Kind == "email_verification" {
			link = base + "/zh-CN/verify-email?token=" + token
		} else {
			link = base + "/zh-CN/reset-password?token=" + token
		}
	}
	body := fmt.Sprintf("To continue, open this link: %s\n", link)
	message := "From: " + cfg.From + "\r\nTo: " + record.Recipient + "\r\nSubject: " + record.Subject + "\r\n\r\n" + body
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return fmt.Errorf("parse identity SMTP sender: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Host+":"+strconv.Itoa(cfg.Port))
	if err != nil {
		return fmt.Errorf("dial identity SMTP server: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	var client *smtp.Client
	if cfg.Port == 465 {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("handshake identity SMTP TLS: %w", err)
		}
		client, err = smtp.NewClient(tlsConn, cfg.Host)
	} else {
		client, err = smtp.NewClient(conn, cfg.Host)
		if err == nil {
			err = client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		}
	}
	if err != nil {
		return fmt.Errorf("connect identity SMTP server: %w", err)
	}
	defer func() { _ = client.Quit() }()
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authenticate identity SMTP: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("set identity SMTP sender: %w", err)
	}
	if err := client.Rcpt(record.Recipient); err != nil {
		return fmt.Errorf("set identity SMTP recipient: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("open identity SMTP data: %w", err)
	}
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write identity SMTP message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish identity SMTP message: %w", err)
	}
	return nil
}

func Probe(ctx context.Context, cfg config.SMTPOptions) error {
	if cfg.Host == "" {
		return nil
	}
	if ctx == nil {
		return errors.New("probe identity SMTP: context is required")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(probeCtx, "tcp", cfg.Host+":"+strconv.Itoa(cfg.Port))
	if err != nil {
		return fmt.Errorf("probe identity SMTP dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	var smtpClient *smtp.Client
	if cfg.Port == 465 {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(probeCtx); err != nil {
			return fmt.Errorf("probe identity SMTP TLS: %w", err)
		}
		smtpClient, err = smtp.NewClient(tlsConn, cfg.Host)
	} else {
		smtpClient, err = smtp.NewClient(conn, cfg.Host)
		if err == nil {
			err = smtpClient.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		}
	}
	if err != nil {
		return fmt.Errorf("probe identity SMTP connection: %w", err)
	}
	defer func() { _ = smtpClient.Quit() }()
	if err := smtpClient.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
		return fmt.Errorf("probe identity SMTP authentication: %w", err)
	}
	return nil
}
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		attempts = 8
	}
	delay := time.Duration(1<<(attempts-1)) * 30 * time.Second
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

var _ coreapp.Component = (*Worker)(nil)

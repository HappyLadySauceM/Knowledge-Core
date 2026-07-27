package nats

import (
	"errors"
	"fmt"

	natsclient "github.com/nats-io/nats.go"
)

func connect(cfg Config) (*natsclient.Conn, error) {
	if cfg.URL == "" {
		return nil, errors.New("connect nats: URL is required")
	}
	if cfg.Name == "" {
		return nil, errors.New("connect nats: client name is required")
	}
	conn, err := natsclient.Connect(
		cfg.URL,
		natsclient.Name(cfg.Name),
		natsclient.Timeout(cfg.ConnectTimeout),
		natsclient.MaxReconnects(cfg.MaxReconnects),
		natsclient.ReconnectWait(cfg.ReconnectWait),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	return conn, nil
}

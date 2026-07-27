package etcd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/foundation/config"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type Config struct {
	Endpoints   []string
	Prefix      string
	Username    string
	Password    string
	DialTimeout time.Duration
}

type Source struct {
	client *clientv3.Client
	prefix string
}

func Open(ctx context.Context, cfg Config) (*Source, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("open etcd config source: endpoints are required")
	}
	if cfg.Prefix == "" {
		return nil, errors.New("open etcd config source: prefix is required")
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   append([]string(nil), cfg.Endpoints...),
		Username:    cfg.Username,
		Password:    cfg.Password,
		DialTimeout: cfg.DialTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("open etcd config source: %w", err)
	}
	source := &Source{client: client, prefix: strings.TrimRight(cfg.Prefix, "/") + "/"}
	if _, err := client.Get(ctx, source.prefix, clientv3.WithPrefix(), clientv3.WithLimit(1)); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("probe etcd config source: %w", err)
	}
	return source, nil
}

func (s *Source) Name() string { return "etcd" }

func (s *Source) Load(ctx context.Context) (config.Snapshot, error) {
	response, err := s.client.Get(ctx, s.prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("load etcd configuration: %w", err)
	}
	snapshot := make(config.Snapshot, len(response.Kvs))
	for _, item := range response.Kvs {
		key := strings.TrimPrefix(string(item.Key), s.prefix)
		if key != "" {
			snapshot[key] = append([]byte(nil), item.Value...)
		}
	}
	return snapshot, nil
}

func (s *Source) Watch(ctx context.Context, onChange config.ChangeHandler) error {
	if onChange == nil {
		return errors.New("watch etcd configuration: change handler is required")
	}
	watch := s.client.Watch(ctx, s.prefix, clientv3.WithPrefix())
	for response := range watch {
		if err := response.Err(); err != nil {
			return fmt.Errorf("watch etcd configuration: %w", err)
		}
		snapshot, err := s.Load(ctx)
		if err != nil {
			return err
		}
		if err := onChange(ctx, snapshot); err != nil {
			return fmt.Errorf("apply etcd configuration: %w", err)
		}
	}
	return ctx.Err()
}

func (s *Source) Close() error {
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("close etcd config source: %w", err)
	}
	return nil
}

var _ config.WatchSource = (*Source)(nil)

package cache

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("cache: key not found")

type KVStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, keys ...string) error
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
	Ping(ctx context.Context) error
	Close() error
}

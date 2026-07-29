package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/internal/cache"
	redisclient "github.com/redis/go-redis/v9"
)

type Config struct {
	Address      string
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Store struct {
	client *redisclient.Client
}

func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Address == "" {
		return nil, errors.New("open redis: address is required")
	}
	client := redisclient.NewClient(&redisclient.Options{
		Addr:         cfg.Address,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})
	store := &Store{client: client}
	if err := store.Ping(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redisclient.Nil) {
		return nil, cache.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}
	return value, nil
}

func (s *Store) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := s.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set: %w", err)
	}
	return nil
}

func (s *Store) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	created, err := s.client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("redis set if absent: %w", err)
	}
	return created, nil
}

func (s *Store) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := s.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis delete: %w", err)
	}
	return nil
}

func (s *Store) Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	result, err := incrementScript.Run(ctx, s.client, []string{key}, delta, ttl.Milliseconds()).Int64()
	if err != nil {
		return 0, fmt.Errorf("redis increment: %w", err)
	}
	return result, nil
}

func (s *Store) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("close redis: %w", err)
	}
	return nil
}

var incrementScript = redisclient.NewScript(`
local value = redis.call('INCRBY', KEYS[1], ARGV[1])
if tonumber(ARGV[2]) > 0 and redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return value
`)

var _ cache.KVStore = (*Store)(nil)

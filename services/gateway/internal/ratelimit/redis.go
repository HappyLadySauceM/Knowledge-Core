package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	redisclient "github.com/redis/go-redis/v9"
)

var incrementScript = redisclient.NewScript(`
local value = redis.call('INCR', KEYS[1])
if value == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return value
`)

type RedisLimiter struct {
	client    redisclient.Scripter
	keyPrefix string
}

func NewRedisLimiter(client *redisclient.Client, keyPrefix string) (*RedisLimiter, error) {
	if client == nil {
		return nil, errors.New("create gateway rate limiter: Redis client is required")
	}
	if keyPrefix == "" || strings.TrimSpace(keyPrefix) != keyPrefix || strings.HasSuffix(keyPrefix, ":") {
		return nil, errors.New("create gateway rate limiter: key prefix is invalid")
	}
	return &RedisLimiter{client: client, keyPrefix: keyPrefix}, nil
}

func (l *RedisLimiter) Consume(
	ctx context.Context,
	scope string,
	subject string,
	now time.Time,
	window time.Duration,
	limit int64,
) (bool, time.Duration, error) {
	if l == nil || l.client == nil || ctx == nil || window < time.Millisecond || limit <= 0 {
		return false, 0, errors.New("consume gateway rate limit: limiter arguments are invalid")
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return false, 0, errors.New("consume gateway rate limit: scope is required")
	}
	now = now.UTC()
	windowMillis := window.Milliseconds()
	bucket := now.UnixMilli() / windowMillis
	bucketEnd := time.UnixMilli((bucket + 1) * windowMillis)
	retryAfter := bucketEnd.Sub(now)
	normalizedSubject, err := normalizeSubject(subject)
	if err != nil {
		return false, 0, fmt.Errorf("consume gateway rate limit: %w", err)
	}
	key := l.keyPrefix + ":" + scope + ":" + strconv.FormatInt(bucket, 10) + ":" + normalizedSubject
	expiryMillis := retryAfter.Milliseconds()
	if expiryMillis < 1 {
		expiryMillis = 1
	}
	count, err := incrementScript.Run(ctx, l.client, []string{key}, expiryMillis).Int64()
	if err != nil {
		return false, 0, fmt.Errorf("consume gateway rate limit: %w", err)
	}
	return count <= limit, retryAfter, nil
}

func normalizeSubject(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if value, found := strings.CutPrefix(raw, "user:"); found {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
			return "", errors.New("rate-limit user subject is invalid")
		}
		return "user:" + value, nil
	}
	return normalizeIP(raw), nil
}

func normalizeIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	ip := net.ParseIP(strings.Trim(raw, "[]"))
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}

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
	client redisclient.Scripter
}

func NewRedisLimiter(client *redisclient.Client) (*RedisLimiter, error) {
	if client == nil {
		return nil, errors.New("create gateway rate limiter: Redis client is required")
	}
	return &RedisLimiter{client: client}, nil
}

func (l *RedisLimiter) Consume(
	ctx context.Context,
	scope string,
	clientIP string,
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
	key := "gateway:rate_limit:" + scope + ":" + strconv.FormatInt(bucket, 10) + ":" + normalizeIP(clientIP)
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

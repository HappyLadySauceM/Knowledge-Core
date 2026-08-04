package ratelimit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	redisclient "github.com/redis/go-redis/v9"
)

func TestNormalizeIP(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:8080":    "127.0.0.1",
		"[2001:db8::1]:443": "2001:db8::1",
		"invalid":           "unknown",
	}
	for input, expected := range tests {
		if got := normalizeIP(input); got != expected {
			t.Errorf("normalizeIP(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestNormalizeSubjectAcceptsOnlyCanonicalPositiveUserID(t *testing.T) {
	if got, err := normalizeSubject("user:42"); err != nil || got != "user:42" {
		t.Fatalf("normalizeSubject(user) = %q, %v", got, err)
	}
	for _, value := range []string{"user:0", "user:+42", "user:042", "user:not-a-number"} {
		if _, err := normalizeSubject(value); err == nil {
			t.Fatalf("normalizeSubject(%q) succeeded", value)
		}
	}
}

func TestConsumeUsesFixedWindowBucket(t *testing.T) {
	client := &scripterStub{counts: make(map[string]int64)}
	limiter := &RedisLimiter{client: client, keyPrefix: "knowledge-core:test:gateway:rate-limit"}
	now := time.Unix(120, 0).UTC()
	allowed, retryAfter, err := limiter.Consume(context.Background(), "auth", "127.0.0.1", now, time.Minute, 2)
	if err != nil || !allowed || retryAfter != time.Minute {
		t.Fatalf("first Consume() = %v, %v, %v", allowed, retryAfter, err)
	}
	allowed, _, err = limiter.Consume(context.Background(), "auth", "127.0.0.1", now.Add(time.Second), time.Minute, 2)
	if err != nil || !allowed {
		t.Fatalf("second Consume() = %v, %v", allowed, err)
	}
	allowed, _, err = limiter.Consume(context.Background(), "auth", "127.0.0.1", now.Add(2*time.Second), time.Minute, 2)
	if err != nil || allowed {
		t.Fatalf("third Consume() = %v, %v", allowed, err)
	}

	allowed, _, err = limiter.Consume(context.Background(), "auth", "127.0.0.1", now.Add(time.Minute), time.Minute, 2)
	if err != nil || !allowed || len(client.counts) != 2 {
		t.Fatalf("next-window Consume() = %v, %v, keys = %#v", allowed, err, client.counts)
	}
	for key := range client.counts {
		if !strings.HasPrefix(key, "knowledge-core:test:gateway:rate-limit:") {
			t.Fatalf("rate-limit key %q is outside the configured environment prefix", key)
		}
	}
}

type scripterStub struct {
	counts map[string]int64
}

func (s *scripterStub) EvalSha(ctx context.Context, _ string, keys []string, _ ...interface{}) *redisclient.Cmd {
	if len(keys) != 1 {
		return redisclient.NewCmdResult(nil, errors.New("expected one key"))
	}
	s.counts[keys[0]]++
	return redisclient.NewCmdResult(s.counts[keys[0]], nil)
}

func (s *scripterStub) Eval(context.Context, string, []string, ...interface{}) *redisclient.Cmd {
	return redisclient.NewCmdResult(nil, errors.New("unexpected Eval"))
}

func (s *scripterStub) EvalRO(context.Context, string, []string, ...interface{}) *redisclient.Cmd {
	return redisclient.NewCmdResult(nil, errors.New("unexpected EvalRO"))
}

func (s *scripterStub) EvalShaRO(context.Context, string, []string, ...interface{}) *redisclient.Cmd {
	return redisclient.NewCmdResult(nil, errors.New("unexpected EvalShaRO"))
}

func (s *scripterStub) ScriptExists(context.Context, ...string) *redisclient.BoolSliceCmd {
	return redisclient.NewBoolSliceCmd(context.Background())
}

func (s *scripterStub) ScriptLoad(context.Context, string) *redisclient.StringCmd {
	return redisclient.NewStringCmd(context.Background())
}

var _ redisclient.Scripter = (*scripterStub)(nil)

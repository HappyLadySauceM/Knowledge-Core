package redis_test

import (
	"context"
	"testing"

	redisadapter "github.com/HappyLadySauce/Knowledge-Core/internal/foundation/cache/redis"
)

func TestOpenRejectsEmptyAddress(t *testing.T) {
	if _, err := redisadapter.Open(context.Background(), redisadapter.Config{}); err == nil {
		t.Fatal("Open() accepted an empty address")
	}
}

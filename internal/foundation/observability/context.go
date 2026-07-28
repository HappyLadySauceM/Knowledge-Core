package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

type contextKey uint8

const (
	requestIDContextKey contextKey = iota
	userIDContextKey
)

func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

func WithUserID(ctx context.Context, userID int64) context.Context {
	if userID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, userIDContextKey, userID)
}

func UserID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	userID, _ := ctx.Value(userIDContextKey).(int64)
	return userID
}

func NewRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("fallback-%d", time.Now().UTC().UnixNano())
}

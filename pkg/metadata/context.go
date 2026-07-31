// Package metadata stores transport-independent request metadata in a context.
package metadata

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const MaxRequestIDLength = 128

type contextKey uint8

const (
	requestIDContextKey contextKey = iota
	userIDContextKey
)

var fallbackSequence atomic.Uint64

// WithRequestID returns a context carrying requestID. Untrusted values that
// could forge log lines or transport headers are ignored.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	requestID = strings.TrimSpace(requestID)
	if !ValidRequestID(requestID) {
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

// EnsureRequestID preserves a valid request ID or installs a newly generated
// one. It is useful at HTTP and RPC ingress boundaries.
func EnsureRequestID(ctx context.Context) context.Context {
	if RequestID(ctx) != "" {
		return ctx
	}
	return WithRequestID(ctx, NewRequestID())
}

func WithUserID(ctx context.Context, userID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
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

// NewRequestID returns a 128-bit, lower-case hexadecimal identifier. The
// fallback also has a fixed, header-safe representation and is only used when
// the operating system random source is unavailable.
func NewRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}

	seed := strconv.FormatInt(time.Now().UTC().UnixNano(), 10) + ":" +
		strconv.FormatUint(fallbackSequence.Add(1), 10)
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

// ValidRequestID accepts visible ASCII identifiers that are safe to copy into
// logs and HTTP/RPC metadata. Spaces, control bytes and non-ASCII bytes are
// intentionally rejected.
func ValidRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > MaxRequestIDLength {
		return false
	}
	for i := 0; i < len(requestID); i++ {
		if requestID[i] < '!' || requestID[i] > '~' {
			return false
		}
	}
	return true
}

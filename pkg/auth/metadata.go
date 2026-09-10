package auth

import (
	"context"
	"strings"

	"github.com/bytedance/gopkg/cloud/metainfo"
)

const accessTokenMetadataKey = "knowledge-core-access-token"

// WithAccessToken attaches a verified bearer token to a trusted internal RPC
// call. The value is sensitive and must never be logged or recorded in spans.
func WithAccessToken(ctx context.Context, value string) context.Context {
	return metainfo.WithPersistentValue(ctx, accessTokenMetadataKey, strings.TrimSpace(value))
}

func AccessToken(ctx context.Context) string {
	value, _ := metainfo.GetPersistentValue(ctx, accessTokenMetadataKey)
	return value
}

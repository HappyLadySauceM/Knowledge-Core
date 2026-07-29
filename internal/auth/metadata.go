package auth

import (
	"context"
	"strings"

	"github.com/bytedance/gopkg/cloud/metainfo"
)

const accessTokenMetadataKey = "knowledge-core-access-token"

// WithAccessToken attaches a verified HTTP bearer token to an internal RPC call.
// The value is transmitted through Kitex persistent metadata and must not be logged.
func WithAccessToken(ctx context.Context, value string) context.Context {
	return metainfo.WithPersistentValue(ctx, accessTokenMetadataKey, strings.TrimSpace(value))
}

// AccessToken reads the raw access token propagated by a trusted gateway.
func AccessToken(ctx context.Context) string {
	value, _ := metainfo.GetPersistentValue(ctx, accessTokenMetadataKey)
	return value
}

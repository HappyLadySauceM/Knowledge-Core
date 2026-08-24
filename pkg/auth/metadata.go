package auth

import (
	"context"
	"strings"

	"github.com/bytedance/gopkg/cloud/metainfo"
)

const accessTokenMetadataKey = "knowledge-core-access-token"
const serviceTokenMetadataKey = "knowledge-core-service-token"

// WithAccessToken attaches a verified bearer token to a trusted internal RPC
// call. The value is sensitive and must never be logged or recorded in spans.
func WithAccessToken(ctx context.Context, value string) context.Context {
	return metainfo.WithPersistentValue(ctx, accessTokenMetadataKey, strings.TrimSpace(value))
}

func AccessToken(ctx context.Context) string {
	value, _ := metainfo.GetPersistentValue(ctx, accessTokenMetadataKey)
	return value
}

// WithServiceToken attaches a service-to-service credential to a trusted RPC.
// The transport propagates it only through TTHeader metadata and application
// code must never log or persist the value.
func WithServiceToken(ctx context.Context, value string) context.Context {
	return metainfo.WithPersistentValue(ctx, serviceTokenMetadataKey, strings.TrimSpace(value))
}

func ServiceToken(ctx context.Context) string {
	value, _ := metainfo.GetPersistentValue(ctx, serviceTokenMetadataKey)
	return value
}

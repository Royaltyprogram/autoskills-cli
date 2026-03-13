package service

import (
	"context"
	"strings"
)

type RequestMetadata struct {
	SourceIP  string
	UserAgent string
}

type requestMetadataContextKey struct{}

func WithRequestMetadata(ctx context.Context, metadata RequestMetadata) context.Context {
	metadata.SourceIP = strings.TrimSpace(metadata.SourceIP)
	metadata.UserAgent = strings.TrimSpace(metadata.UserAgent)
	return context.WithValue(ctx, requestMetadataContextKey{}, metadata)
}

func RequestMetadataFromContext(ctx context.Context) (RequestMetadata, bool) {
	metadata, ok := ctx.Value(requestMetadataContextKey{}).(RequestMetadata)
	return metadata, ok
}

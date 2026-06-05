package artifacts

import (
	"context"
	"io"
)

// Store handles persistence and retrieval of artifact blobs.
// Keys are opaque strings (typically UUID strings or legacy absolute paths).
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

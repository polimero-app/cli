package protocoltrace

import (
	"context"

	"github.com/polimero-app/cli/internal/apperr"
)

// Setup opens a protocol trace file if path is non-empty and returns the
// enriched context and a cleanup function. The caller must call cleanup
// after protocol work completes (even on error).
//
// If path is empty, Setup returns the original context and a no-op cleanup.
// If the trace file cannot be created, Setup returns an exit-code-2 error.
func Setup(ctx context.Context, path string) (context.Context, func() error, error) {
	if path == "" {
		return ctx, func() error { return nil }, nil
	}
	sink, err := NewFileSink(path)
	if err != nil {
		return ctx, nil, apperr.Wrap(2, err.Error(), err)
	}
	ctx = WithSink(ctx, sink)
	cleanup := func() error {
		return sink.Close()
	}
	return ctx, cleanup, nil
}

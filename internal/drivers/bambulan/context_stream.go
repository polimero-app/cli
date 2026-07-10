package bambulan

import (
	"context"
	"io"
	"sync"
)

// contextStream closes its underlying stream when the operation context is
// canceled. Explicit Close stops the watcher and is safe to call repeatedly.
type contextStream struct {
	io.ReadCloser
	done     chan struct{}
	closeErr error
	once     sync.Once
}

func bindStreamToContext(ctx context.Context, stream io.ReadCloser) io.ReadCloser {
	bound := &contextStream{ReadCloser: stream, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = bound.Close()
		case <-bound.done:
		}
	}()
	return bound
}

func (s *contextStream) Close() error {
	s.once.Do(func() {
		close(s.done)
		s.closeErr = s.ReadCloser.Close()
	})
	return s.closeErr
}

package bambulan

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type trackedReadCloser struct {
	closed chan struct{}
	calls  atomic.Int32
}

func (s *trackedReadCloser) Read([]byte) (int, error) { return 0, io.EOF }

func (s *trackedReadCloser) Close() error {
	if s.calls.Add(1) == 1 {
		close(s.closed)
	}
	return nil
}

func TestBindStreamToContext_CancelClosesStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	underlying := &trackedReadCloser{closed: make(chan struct{})}
	stream := bindStreamToContext(ctx, underlying)
	cancel()

	select {
	case <-underlying.closed:
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not close stream")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}
	if calls := underlying.calls.Load(); calls != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", calls)
	}
}

func TestBindStreamToContext_ExplicitCloseStopsWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	underlying := &trackedReadCloser{closed: make(chan struct{})}
	stream := bindStreamToContext(ctx, underlying)
	if err := stream.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	cancel()
	time.Sleep(10 * time.Millisecond)
	if calls := underlying.calls.Load(); calls != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", calls)
	}
}

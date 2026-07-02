package protocoltrace

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Sink is the interface for writing protocol trace events.
// Drivers receive a Sink through context and emit events without knowing
// whether a trace file is active.
type Sink interface {
	// Emit writes a single trace event.
	// Implementations must be safe for concurrent use.
	Emit(e Event)
}

// FromContext extracts the Sink from ctx, or returns a no-op sink if absent.
func FromContext(ctx context.Context) Sink {
	if s, ok := ctx.Value(sinkKey{}).(Sink); ok && s != nil {
		return s
	}
	return nopSink{}
}

// WithSink returns a derived context carrying the given Sink.
func WithSink(ctx context.Context, s Sink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

type sinkKey struct{}

// nopSink discards all events.
type nopSink struct{}

func (nopSink) Emit(Event) {}

// FileSink writes JSON Lines events to an os.File.
// It is safe for concurrent use.
type FileSink struct {
	mu       sync.Mutex
	w        io.WriteCloser
	enc      *json.Encoder
	writeErr error // first Emit failure, surfaced by Close
}

// NewFileSink creates a trace file at path with mode 0600.
// The path must not already exist; an existing file causes an error.
// Returns the open FileSink ready for Emit calls.
func NewFileSink(path string) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("protocol trace file already exists: %s", path)
		}
		return nil, fmt.Errorf("cannot create protocol trace file: %w", err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &FileSink{w: f, enc: enc}, nil
}

// Emit writes a single event as one JSON line.
// The first failure is recorded and reported by Close so the command
// layer can fail the invocation instead of silently truncating traces.
func (fs *FileSink) Emit(e Event) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if err := fs.enc.Encode(e); err != nil && fs.writeErr == nil {
		fs.writeErr = err
	}
}

// Close flushes and closes the underlying file. It returns the first
// Emit failure, if any, otherwise the close error.
func (fs *FileSink) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	closeErr := fs.w.Close()
	if fs.writeErr != nil {
		return fmt.Errorf("protocol trace write failed: %w", fs.writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("protocol trace close failed: %w", closeErr)
	}
	return nil
}

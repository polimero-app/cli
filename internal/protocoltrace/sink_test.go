package protocoltrace_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/protocoltrace"
)

func TestNewFileSink_CreatesFileWithPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	sink, err := protocoltrace.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}
	defer func() { _ = sink.Close() }()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat trace file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %04o, want 0600", perm)
	}
}

func TestNewFileSink_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	// Create the file first.
	if err := os.WriteFile(path, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := protocoltrace.NewFileSink(path)
	if err == nil {
		t.Fatal("NewFileSink() should fail when file exists")
	}
	if got := err.Error(); got == "" {
		t.Error("error message should not be empty")
	}
}

func TestNewFileSink_ErrorOnBadPath(t *testing.T) {
	_, err := protocoltrace.NewFileSink("/nonexistent-dir-xyz/trace.jsonl")
	if err == nil {
		t.Fatal("NewFileSink() should fail for nonexistent directory")
	}
}

func TestFileSink_EmitWritesJSONLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	sink, err := protocoltrace.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}

	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	dur := int64(42)

	sink.Emit(protocoltrace.Event{
		Timestamp:  ts,
		Command:    "status",
		Driver:     "bambu-lan",
		Operation:  "Status",
		Phase:      "connect",
		Transport:  "mqtt",
		Endpoint:   "192.168.1.10:8883",
		DurationMs: &dur,
	})
	sink.Emit(protocoltrace.Event{
		Timestamp: ts,
		Command:   "status",
		Driver:    "bambu-lan",
		Operation: "Status",
		Phase:     "subscribe",
	})

	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	var events []protocoltrace.Event
	for scanner.Scan() {
		var ev protocoltrace.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal line: %v", err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Phase != "connect" {
		t.Errorf("event[0].Phase = %q, want %q", events[0].Phase, "connect")
	}
	if events[0].DurationMs == nil || *events[0].DurationMs != 42 {
		t.Errorf("event[0].DurationMs = %v, want 42", events[0].DurationMs)
	}
	if events[1].Phase != "subscribe" {
		t.Errorf("event[1].Phase = %q, want %q", events[1].Phase, "subscribe")
	}
}

func TestFileSink_ConcurrentEmit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	sink, err := protocoltrace.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink() error = %v", err)
	}

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			sink.Emit(protocoltrace.Event{
				Timestamp: time.Now().UTC(),
				Phase:     "concurrent",
				Detail:    map[string]any{"idx": idx},
			})
		}(i)
	}
	wg.Wait()

	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != n {
		t.Errorf("got %d lines, want %d", lines, n)
	}
}

func TestFromContext_ReturnsNopWhenAbsent(t *testing.T) {
	ctx := context.Background()
	sink := protocoltrace.FromContext(ctx)
	// Should not panic.
	sink.Emit(protocoltrace.Event{Timestamp: time.Now()})
}

func TestWithSink_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")

	fileSink, err := protocoltrace.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fileSink.Close() }()

	ctx := protocoltrace.WithSink(context.Background(), fileSink)
	got := protocoltrace.FromContext(ctx)

	// Emit through the retrieved sink and verify it lands in the file.
	got.Emit(protocoltrace.Event{
		Timestamp: time.Now().UTC(),
		Phase:     "test-roundtrip",
	})
	_ = fileSink.Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected data in trace file after Emit through FromContext")
	}
}

package protocoltrace_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/polimero-app/cli/internal/protocoltrace"
)

func TestSetup_EmptyPath_NopContext(t *testing.T) {
	ctx := context.Background()
	gotCtx, cleanup, err := protocoltrace.Setup(ctx, "")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if gotCtx != ctx {
		t.Error("expected original context returned for empty path")
	}
	// Cleanup should be callable and not error.
	if err := cleanup(); err != nil {
		t.Errorf("cleanup() error = %v", err)
	}
}

func TestSetup_ValidPath_CreatesSink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	ctx := context.Background()

	gotCtx, cleanup, err := protocoltrace.Setup(ctx, path)
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	// Context should have a sink.
	sink := protocoltrace.FromContext(gotCtx)
	sink.Emit(protocoltrace.Event{Phase: "test"})

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("trace file should not be empty after Emit")
	}
}

func TestSetup_ExistingFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := protocoltrace.Setup(context.Background(), path)
	if err == nil {
		t.Fatal("Setup() should fail for existing file")
	}
}

func TestSetup_BadDir_ReturnsError(t *testing.T) {
	_, _, err := protocoltrace.Setup(context.Background(), "/nonexistent-dir-xyz/trace.jsonl")
	if err == nil {
		t.Fatal("Setup() should fail for nonexistent directory")
	}
}

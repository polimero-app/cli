package protocoltrace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
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

func TestFileSink_EmitFailure_SurfacedByClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	sink, err := protocoltrace.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	// Invalid raw JSON payload makes Encode fail.
	sink.Emit(protocoltrace.Event{Driver: "d", Payload: json.RawMessage("{not json")})
	closeErr := sink.Close()
	if closeErr == nil {
		t.Fatal("Close() should surface the Emit encode failure")
	}
	if !strings.Contains(closeErr.Error(), "protocol trace write failed") {
		t.Errorf("Close() error = %q, want write-failed message", closeErr)
	}
}

func TestFileSink_CleanClose_NoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.jsonl")
	sink, err := protocoltrace.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	sink.Emit(protocoltrace.Event{Driver: "d"})
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
}

func TestFinish_CleanupOK_LeavesRetErr(t *testing.T) {
	var retErr error
	var buf bytes.Buffer
	protocoltrace.Finish(func() error { return nil }, &buf, &retErr)
	if retErr != nil || buf.Len() != 0 {
		t.Errorf("Finish with clean cleanup: retErr=%v out=%q", retErr, buf.String())
	}
}

func TestFinish_CleanupFails_SetsExit1(t *testing.T) {
	var retErr error
	var buf bytes.Buffer
	protocoltrace.Finish(func() error { return errors.New("boom") }, &buf, &retErr)
	if retErr == nil {
		t.Fatal("Finish should set retErr on cleanup failure")
	}
	var exitErr *apperr.ExitError
	if !errors.As(retErr, &exitErr) || exitErr.Code != 1 {
		t.Errorf("retErr = %v, want ExitError code 1", retErr)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("errOut = %q, want cleanup error message", buf.String())
	}
}

func TestFinish_EarlierErrorWins(t *testing.T) {
	earlier := apperr.New(4, "network down")
	retErr := error(earlier)
	var buf bytes.Buffer
	protocoltrace.Finish(func() error { return errors.New("boom") }, &buf, &retErr)
	if retErr != error(earlier) {
		t.Errorf("retErr = %v, want earlier error preserved", retErr)
	}
	if !strings.Contains(buf.String(), "boom") {
		t.Errorf("errOut = %q, want cleanup failure still reported", buf.String())
	}
}

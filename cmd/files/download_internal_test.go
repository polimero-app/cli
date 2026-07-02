package files

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
)

func TestCommitDownloadFile_NoOverwrite_PreExistingDest_NotClobbered(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.3mf")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}
	tmp := filepath.Join(dir, ".polimero-download-test")
	if err := os.WriteFile(tmp, []byte("new-data"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	err := commitDownloadFile(tmp, dest, false)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}

	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read dest: %v", readErr)
	}
	if string(got) != "original" {
		t.Fatalf("dest was overwritten: got %q, want %q", got, "original")
	}
}

func TestCommitDownloadFile_Overwrite_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.3mf")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}
	tmp := filepath.Join(dir, ".polimero-download-test")
	if err := os.WriteFile(tmp, []byte("new-data"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	if err := commitDownloadFile(tmp, dest, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read dest: %v", readErr)
	}
	if string(got) != "new-data" {
		t.Fatalf("dest not replaced: got %q", got)
	}
}

func TestCommitDownloadFile_NoOverwrite_FreshDest_MovesFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.3mf")
	tmp := filepath.Join(dir, ".polimero-download-test")
	if err := os.WriteFile(tmp, []byte("new-data"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	if err := commitDownloadFile(tmp, dest, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read dest: %v", readErr)
	}
	if string(got) != "new-data" {
		t.Fatalf("unexpected dest content: %q", got)
	}
	if _, statErr := os.Stat(tmp); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("temp file not cleaned up: %v", statErr)
	}
}

package camera

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
)

func TestCommitSnapshotFile_NoOverwrite_PreExistingDest_NotClobbered(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.jpg")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}
	tmp := filepath.Join(dir, ".tmp-snapshot")
	if err := os.WriteFile(tmp, []byte("new-data"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	err := commitSnapshotFile(tmp, dest, false)
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

func TestCommitSnapshotFile_Overwrite_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.jpg")
	if err := os.WriteFile(dest, []byte("original"), 0o644); err != nil {
		t.Fatalf("seed dest: %v", err)
	}
	tmp := filepath.Join(dir, ".tmp-snapshot")
	if err := os.WriteFile(tmp, []byte("new-data"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	if err := commitSnapshotFile(tmp, dest, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read dest: %v", readErr)
	}
	if string(got) != "new-data" {
		t.Fatalf("dest = %q, want %q", got, "new-data")
	}
}

func TestCommitSnapshotFile_NoOverwrite_NewDest_Created(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.jpg")
	tmp := filepath.Join(dir, ".tmp-snapshot")
	if err := os.WriteFile(tmp, []byte("new-data"), 0o644); err != nil {
		t.Fatalf("seed tmp: %v", err)
	}

	if err := commitSnapshotFile(tmp, dest, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("read dest: %v", readErr)
	}
	if string(got) != "new-data" {
		t.Fatalf("dest = %q, want %q", got, "new-data")
	}
	if _, statErr := os.Stat(tmp); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected tmp file to be gone, stat err = %v", statErr)
	}
}

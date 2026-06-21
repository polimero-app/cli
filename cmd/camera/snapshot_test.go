package camera_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polimero-app/cli/cmd/camera"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
)

func snapshotDriver() *stubDriver {
	return &stubDriver{
		caps: driver.Capabilities{CameraSnapshot: true},
		snapshotRes: &driver.CameraSnapshotResult{
			Data:         []byte{0xFF, 0xD8, 'o', 'k', 0xFF, 0xD9},
			Protocol:     "mjpeg",
			Capabilities: driver.Capabilities{CameraSnapshot: true},
		},
	}
}

func runSnapshotCmd(t *testing.T, deps camera.Deps, args ...string) (string, error) {
	t.Helper()
	root := testRoot(deps)
	buf := &strings.Builder{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"camera", "snapshot"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func TestSnapshot_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, snapshotDriver())
	out, err := runSnapshotCmd(t, deps)
	if err != nil {
		t.Errorf("expected no error (help), got %v", err)
	}
	if !strings.Contains(out, "snapshot <name>") {
		t.Errorf("expected usage line in help output:\n%s", out)
	}
}

func TestSnapshot_ToFile_HumanOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, snapshotDriver())
	dest := filepath.Join(t.TempDir(), "snapshot.jpg")

	out, err := runSnapshotCmd(t, deps, "myprinter", "--to", dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if string(data) != string(snapshotDriver().snapshotRes.Data) {
		t.Fatalf("snapshot data = %v, want %v", data, snapshotDriver().snapshotRes.Data)
	}
	if !strings.Contains(out, "Snapshot saved to "+dest) {
		t.Errorf("expected saved message, got:\n%s", out)
	}
}

func TestSnapshot_ToDirectory_UsesGeneratedName(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, snapshotDriver())
	destDir := t.TempDir()

	_, err := runSnapshotCmd(t, deps, "myprinter", "--to", destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries, err := os.ReadDir(destDir)
	if err != nil {
		t.Fatalf("read destination dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one snapshot file, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "myprinter-") || !strings.HasSuffix(name, ".jpg") {
		t.Fatalf("generated file name = %q, want myprinter-*.jpg", name)
	}
}

func TestSnapshot_DefaultDestination_UsesCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, snapshotDriver())
	cwd := t.TempDir()
	t.Chdir(cwd)

	_, err := runSnapshotCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(cwd, "myprinter-*.jpg"))
	if err != nil {
		t.Fatalf("glob snapshots: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected one generated snapshot, got %d", len(matches))
	}
}

func TestSnapshot_DestinationExistsWithoutOverwrite_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, snapshotDriver())
	dest := filepath.Join(t.TempDir(), "snapshot.jpg")
	if err := os.WriteFile(dest, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runSnapshotCmd(t, deps, "myprinter", "--to", dest)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	data, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "existing" {
		t.Fatalf("existing file was overwritten without --overwrite")
	}
}

func TestSnapshot_OverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := snapshotDriver()
	deps := makeDeps(t, dir, kc, drv)
	dest := filepath.Join(t.TempDir(), "snapshot.jpg")
	if err := os.WriteFile(dest, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runSnapshotCmd(t, deps, "myprinter", "--to", dest, "--overwrite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != string(drv.snapshotRes.Data) {
		t.Fatalf("snapshot data = %v, want %v", data, drv.snapshotRes.Data)
	}
}

func TestSnapshot_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, snapshotDriver())
	dest := filepath.Join(t.TempDir(), "snapshot.jpg")

	out, err := runSnapshotCmd(t, deps, "myprinter", "--to", dest, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Profile   string `json:"profile"`
			Driver    string `json:"driver"`
			Path      string `json:"path"`
			SizeBytes int    `json:"sizeBytes"`
			Protocol  string `json:"protocol"`
		} `json:"data"`
		Meta struct {
			Command    string `json:"command"`
			DurationMs *int   `json:"durationMs"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, out)
	}
	if !env.OK {
		t.Fatal("expected ok=true")
	}
	if env.Data.Profile != "myprinter" || env.Data.Driver != "bambu-lan" || env.Data.Path != dest {
		t.Fatalf("unexpected data: %+v", env.Data)
	}
	if env.Data.SizeBytes != len(snapshotDriver().snapshotRes.Data) {
		t.Fatalf("sizeBytes = %d, want %d", env.Data.SizeBytes, len(snapshotDriver().snapshotRes.Data))
	}
	if env.Data.Protocol != "mjpeg" {
		t.Fatalf("protocol = %q, want mjpeg", env.Data.Protocol)
	}
	if env.Meta.Command != "camera snapshot" {
		t.Fatalf("command = %q, want camera snapshot", env.Meta.Command)
	}
	if env.Meta.DurationMs == nil {
		t.Fatal("expected durationMs")
	}
}

func TestSnapshot_JSONError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{caps: driver.Capabilities{CameraSnapshot: false}}
	deps := makeDeps(t, dir, kc, drv)

	out, _ := runSnapshotCmd(t, deps, "myprinter", "--output", "json")
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, out)
	}
	if env.OK {
		t.Fatal("expected ok=false")
	}
	if env.Error.Code != "capability_unsupported" {
		t.Fatalf("error code = %q, want capability_unsupported", env.Error.Code)
	}
	if env.Meta.Command != "camera snapshot" {
		t.Fatalf("command = %q, want camera snapshot", env.Meta.Command)
	}
}

func TestSnapshot_MissingAccessCode_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:access-code")
	deps := makeDeps(t, dir, kc, snapshotDriver())

	_, err := runSnapshotCmd(t, deps, "myprinter", "--to", filepath.Join(t.TempDir(), "snapshot.jpg"))
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("expected exit 3, got %v", err)
	}
}

func TestSnapshot_InsecureSkipsTLSFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint")
	deps := makeDeps(t, dir, kc, snapshotDriver())

	_, err := runSnapshotCmd(t, deps, "myprinter", "--insecure", "--to", filepath.Join(t.TempDir(), "snapshot.jpg"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSnapshot_DoesNotLeakAccessCode(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, snapshotDriver())

	out, _ := runSnapshotCmd(t, deps, "myprinter", "--to", filepath.Join(t.TempDir(), "snapshot.jpg"))
	if strings.Contains(out, "testcode") {
		t.Fatal("output contains access code")
	}
}

package files_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/files"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubFileDriver satisfies driver.FileDriver for files command tests.
type stubFileDriver struct {
	caps         driver.Capabilities
	rootsResult  []driver.FileRoot
	rootsErr     error
	listResult   *driver.FileListResult
	listErr      error
	downloadData []byte
	downloadErr  error
	uploadResult *driver.FileTransferResult
	uploadErr    error
}

func (s *stubFileDriver) Name() string                      { return "bambu-lan" }
func (s *stubFileDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubFileDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubFileDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubFileDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, nil
}
func (s *stubFileDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, nil
}
func (s *stubFileDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, nil
}
func (s *stubFileDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubFileDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return nil, nil
}
func (s *stubFileDriver) FileRoots(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) ([]driver.FileRoot, error) {
	return s.rootsResult, s.rootsErr
}
func (s *stubFileDriver) FileList(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ string, _ string, _ bool, _ *slog.Logger) (*driver.FileListResult, error) {
	return s.listResult, s.listErr
}
func (s *stubFileDriver) FileDownload(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ string, _ string, dst io.Writer, _ *slog.Logger) (*driver.FileTransferResult, error) {
	if s.downloadErr != nil {
		return nil, s.downloadErr
	}
	n, _ := dst.Write(s.downloadData)
	n64 := int64(n)
	return &driver.FileTransferResult{BytesTransferred: &n64}, nil
}
func (s *stubFileDriver) FileUpload(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ string, _ string, src io.Reader, _ int64, _ bool, _ *slog.Logger) (*driver.FileTransferResult, error) {
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	data, _ := io.ReadAll(src)
	n := int64(len(data))
	if s.uploadResult != nil {
		return s.uploadResult, nil
	}
	return &driver.FileTransferResult{BytesTransferred: &n}, nil
}

func defaultFileDriver() *stubFileDriver {
	size := int64(240640)
	mod := "2026-06-15T12:34:00Z"
	return &stubFileDriver{
		caps: driver.Capabilities{FileList: true, FileDownload: true, FileUpload: true},
		rootsResult: []driver.FileRoot{
			{
				Name:        "sdcard",
				Description: "SD card",
				Writable:    true,
				Metadata:    map[string]any{},
			},
		},
		listResult: &driver.FileListResult{
			Entries: []driver.FileEntry{
				{
					Name:       "models",
					Root:       "sdcard",
					Path:       "/models",
					DevicePath: "sdcard:/models",
					Type:       driver.FileEntryTypeDirectory,
					Metadata:   map[string]any{},
				},
				{
					Name:       "calibration-cube.3mf",
					Root:       "sdcard",
					Path:       "/calibration-cube.3mf",
					DevicePath: "sdcard:/calibration-cube.3mf",
					Type:       driver.FileEntryTypeFile,
					SizeBytes:  &size,
					ModifiedAt: &mod,
					Metadata:   map[string]any{},
				},
			},
		},
		downloadData: []byte("file-contents-here"),
	}
}

func seedProfile(t *testing.T, dir string, kc *keychain.Mock, name string, insecure bool) {
	t.Helper()
	now := time.Now().UTC()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.AddProfile(name, config.Profile{
		Driver:   "bambu-lan",
		Host:     "192.0.2.10",
		Serial:   "SN001",
		Timeout:  "10s",
		Insecure: insecure,
		Created:  now,
		Updated:  now,
	})
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":access-code", "testcode")
	if !insecure {
		_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":tls-fingerprint", testFingerprint)
	}
}

func makeDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver) files.Deps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return files.Deps{
		KC: kc,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" && drv != nil {
				return drv, true
			}
			return nil, false
		},
		Log: slog.Default(),
	}
}

func testRoot(deps files.Deps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(files.CommandWithDeps(deps))
	return root
}

func runCmd(t *testing.T, deps files.Deps, args ...string) (string, error) {
	t.Helper()
	root := testRoot(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"files"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Roots tests ---

func TestRoots_Human(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, err := runCmd(t, deps, "roots", "garage-x1c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sdcard") {
		t.Errorf("expected 'sdcard' in output:\n%s", out)
	}
	if !strings.Contains(out, "SD card") {
		t.Errorf("expected description in output:\n%s", out)
	}
}

func TestRoots_JSON(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, err := runCmd(t, deps, "roots", "garage-x1c", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON:\n%s\nerror: %v", out, err)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env["ok"])
	}
	data := env["data"].(map[string]any)
	if data["profile"] != "garage-x1c" {
		t.Errorf("expected profile=garage-x1c, got %v", data["profile"])
	}
	roots := data["roots"].([]any)
	if len(roots) != 1 {
		t.Errorf("expected 1 root, got %d", len(roots))
	}
}

func TestRoots_ProfileNotFound_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)

	_, err := runCmd(t, deps, "roots", "nonexistent")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestRoots_AccessCodeMissing_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)

	// Seed profile but not access code.
	now := time.Now().UTC()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("nocode", config.Profile{
		Driver: "bambu-lan", Host: "192.0.2.10", Serial: "SN001", Timeout: "10s", Created: now, Updated: now,
	})
	_ = config.Save(dir, cfg)

	_, err := runCmd(t, deps, "roots", "nocode")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestRoots_Insecure_SkipsTLSFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "insecure-printer", true)

	out, err := runCmd(t, deps, "roots", "insecure-printer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sdcard") {
		t.Errorf("expected output, got:\n%s", out)
	}
}

func TestRoots_UnsupportedCapability_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := &stubFileDriver{caps: driver.Capabilities{}} // no FileList
	drv.rootsResult = []driver.FileRoot{}
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "nofiles", false)

	_, err := runCmd(t, deps, "roots", "nofiles")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit 5 (unsupported capability), got %v", err)
	}
}

// --- List tests ---

func TestList_Human_DefaultRoot(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, err := runCmd(t, deps, "list", "garage-x1c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "models") {
		t.Errorf("expected 'models' in output:\n%s", out)
	}
	if !strings.Contains(out, "calibration-cube.3mf") {
		t.Errorf("expected 'calibration-cube.3mf' in output:\n%s", out)
	}
}

func TestList_JSON(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, err := runCmd(t, deps, "list", "garage-x1c", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON:\n%s\nerror: %v", out, err)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env["ok"])
	}
	data := env["data"].(map[string]any)
	paths := data["paths"].([]any)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path result, got %d", len(paths))
	}
	pathData := paths[0].(map[string]any)
	entries := pathData["entries"].([]any)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestList_WithDevicePath(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, err := runCmd(t, deps, "list", "garage-x1c", "sdcard:/models")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Path: sdcard:/models") {
		t.Errorf("expected path header in output:\n%s", out)
	}
}

func TestList_InvalidDevicePath_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	_, err := runCmd(t, deps, "list", "garage-x1c", "sdcard:/../etc/passwd")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestList_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := &stubFileDriver{
		caps:        driver.Capabilities{FileList: true},
		rootsResult: []driver.FileRoot{{Name: "sdcard", Description: "SD card", Writable: true, Metadata: map[string]any{}}},
		listResult:  &driver.FileListResult{Entries: []driver.FileEntry{}},
	}
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, err := runCmd(t, deps, "list", "garage-x1c", "sdcard:/empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "(empty)") {
		t.Errorf("expected '(empty)' in output:\n%s", out)
	}
}

// --- Download tests ---

func TestDownload_Human(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	// Use a temp directory as --to destination.
	destDir := t.TempDir()
	out, err := runCmd(t, deps, "download", "garage-x1c", "sdcard:/calibration-cube.3mf", "--to", destDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Downloaded") {
		t.Errorf("expected 'Downloaded' in output:\n%s", out)
	}
	if !strings.Contains(out, "calibration-cube.3mf") {
		t.Errorf("expected filename in output:\n%s", out)
	}
}

func TestDownload_DirectoryPath_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	_, err := runCmd(t, deps, "download", "garage-x1c", "sdcard:/")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 (cannot download directory), got %v", err)
	}
}

func TestDownload_JSON(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	destDir := t.TempDir()
	out, err := runCmd(t, deps, "download", "garage-x1c", "sdcard:/cube.3mf", "--to", destDir, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON:\n%s\nerror: %v", out, err)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env["ok"])
	}
}

// --- Upload tests ---

func TestUpload_MissingLocalFile_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	_, err := runCmd(t, deps, "upload", "garage-x1c", "/nonexistent/file.3mf", "sdcard:/file.3mf")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestUpload_InvalidDevicePath_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	// Create a temp file to upload.
	tmpFile := dir + "/test.3mf"
	_ = writeTestFile(t, tmpFile, "test data")

	_, err := runCmd(t, deps, "upload", "garage-x1c", tmpFile, "sdcard:/../evil")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

// --- Error JSON envelope tests ---

func TestFiles_ErrorJSON_Envelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := defaultFileDriver()
	deps := makeDeps(t, dir, kc, drv)

	out, _ := runCmd(t, deps, "roots", "nonexistent", "--output", "json")
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON:\n%s\nerror: %v", out, err)
	}
	if env["ok"] != false {
		t.Errorf("expected ok=false, got %v", env["ok"])
	}
	if env["error"] == nil {
		t.Error("expected error object in envelope")
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] == nil || errObj["code"] == "" {
		t.Error("expected error code")
	}
	if errObj["message"] == nil || errObj["message"] == "" {
		t.Error("expected error message")
	}
}

func TestFiles_DoesNotLeakSecrets(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	drv := &stubFileDriver{
		caps:     driver.Capabilities{FileList: true},
		rootsErr: apperr.New(4, "connection failed"),
	}
	deps := makeDeps(t, dir, kc, drv)
	seedProfile(t, dir, kc, "garage-x1c", false)

	out, _ := runCmd(t, deps, "roots", "garage-x1c", "--output", "json")
	if strings.Contains(out, "testcode") {
		t.Error("output contains access code secret")
	}
	if strings.Contains(out, testFingerprint) {
		t.Error("output contains TLS fingerprint")
	}
}

// --- Helpers ---

func writeTestFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o644)
}

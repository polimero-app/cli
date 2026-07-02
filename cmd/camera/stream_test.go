package camera_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/camera"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubDriver satisfies driver.Driver for camera stream command tests.
type stubDriver struct {
	caps        driver.Capabilities
	streamRes   *driver.CameraStreamResult
	streamErr   error
	snapshotRes *driver.CameraSnapshotResult
	snapshotErr error
}

func (s *stubDriver) Name() string                      { return "bambu-lan" }
func (s *stubDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, nil
}
func (s *stubDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return s.streamRes, s.streamErr
}
func (s *stubDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return s.snapshotRes, s.snapshotErr
}
func (s *stubDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}

func defaultDriver() *stubDriver {
	return &stubDriver{
		caps: driver.Capabilities{CameraStream: true},
		streamRes: &driver.CameraStreamResult{
			Format:       driver.CameraFormatMJPEG,
			Stream:       io.NopCloser(strings.NewReader("fake-mjpeg-data")),
			Capabilities: driver.Capabilities{CameraStream: true},
		},
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

func makeDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver) camera.Deps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return camera.Deps{
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

func testRoot(deps camera.Deps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(camera.CommandWithDeps(deps))
	return root
}

func runCmd(t *testing.T, deps camera.Deps, args ...string) (string, error) {
	t.Helper()
	root := testRoot(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"camera", "stream"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Tests ---

func TestStream_NoArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
	if !strings.Contains(out, "profile name is required") {
		t.Errorf("expected usage error message, got:\n%s", out)
	}
}

func TestStream_TooManyArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "one", "two")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStream_InvalidProfileName_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "_invalid")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStream_ProfileNotFound_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "nonexistent")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStream_MissingAccessCode_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:access-code")
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStream_MissingTLSFingerprint_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint")
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStream_InsecureSkipsTLSFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint")

	drv := defaultDriver()
	deps := makeDeps(t, dir, kc, drv)

	// With --insecure, missing fingerprint should not cause exit 3.
	// Instead it should proceed to the driver and start serving.
	// We use --timeout 1ms to make the server stop quickly.
	port := freePort(t)
	out, err := runCmd(t, deps, "myprinter", "--insecure", "--timeout", "1ms", "--port", portStr(port))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Streaming camera from myprinter") {
		t.Errorf("expected streaming message, got:\n%s", out)
	}
}

func TestStream_InsecureProfile_SkipsTLSFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	// No fingerprint stored, profile is insecure.

	drv := defaultDriver()
	deps := makeDeps(t, dir, kc, drv)

	port := freePort(t)
	out, err := runCmd(t, deps, "myprinter", "--timeout", "1ms", "--port", portStr(port))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Streaming camera from myprinter") {
		t.Errorf("expected streaming message, got:\n%s", out)
	}
}

func TestStream_DriverUnsupported_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{
		caps: driver.Capabilities{CameraStream: false, Status: true},
	}
	deps := makeDeps(t, dir, kc, drv)
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit 5, got %v", err)
	}
}

func TestStream_DriverNetworkError_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{
		caps:      driver.Capabilities{CameraStream: true},
		streamErr: apperr.New(4, "camera endpoint unreachable: both ports 6000 and 322 failed"),
	}
	deps := makeDeps(t, dir, kc, drv)
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestStream_InvalidPort_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--port", "0")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStream_InvalidTimeout_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--timeout", "notaduration")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStream_ZeroTimeout_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--timeout", "0s")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStream_PortInUse_ExitsCode2(t *testing.T) {
	// Occupy a port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, cmdErr := runCmd(t, deps, "myprinter", "--port", portStr(port), "--timeout", "1ms")
	var exitErr *apperr.ExitError
	if !errors.As(cmdErr, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for port in use, got %v", cmdErr)
	}
}

func TestStream_MJPEG_HumanOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{
		caps: driver.Capabilities{CameraStream: true},
		streamRes: &driver.CameraStreamResult{
			Format:       driver.CameraFormatMJPEG,
			Stream:       io.NopCloser(strings.NewReader("fake-data")),
			Capabilities: driver.Capabilities{CameraStream: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)
	port := freePort(t)
	out, err := runCmd(t, deps, "myprinter", "--timeout", "1ms", "--port", portStr(port))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "MJPEG (open in browser)") {
		t.Errorf("expected MJPEG format description, got:\n%s", out)
	}
	if !strings.Contains(out, "Stream stopped.") {
		t.Errorf("expected 'Stream stopped.' on clean exit, got:\n%s", out)
	}
}

func TestStream_H264_HumanOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{
		caps: driver.Capabilities{CameraStream: true},
		streamRes: &driver.CameraStreamResult{
			Format:       driver.CameraFormatH264,
			Stream:       io.NopCloser(strings.NewReader("fake-h264")),
			Capabilities: driver.Capabilities{CameraStream: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)
	port := freePort(t)
	out, err := runCmd(t, deps, "myprinter", "--timeout", "1ms", "--port", portStr(port))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "H.264 (open with VLC or mpv)") {
		t.Errorf("expected H.264 format description, got:\n%s", out)
	}
}

func TestStream_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{
		caps: driver.Capabilities{CameraStream: true},
		streamRes: &driver.CameraStreamResult{
			Format:       driver.CameraFormatMJPEG,
			Stream:       io.NopCloser(strings.NewReader("fake-data")),
			Capabilities: driver.Capabilities{CameraStream: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)
	port := freePort(t)
	out, err := runCmd(t, deps, "myprinter", "--output", "json", "--timeout", "1ms", "--port", portStr(port))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Profile string `json:"profile"`
			URL     string `json:"url"`
			Format  string `json:"format"`
			Port    int    `json:"port"`
		} `json:"data"`
		Meta struct {
			Command string `json:"command"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, out)
	}
	if !env.OK {
		t.Errorf("expected ok=true")
	}
	if env.Data.Profile != "myprinter" {
		t.Errorf("expected profile=myprinter, got %q", env.Data.Profile)
	}
	if env.Data.Format != "mjpeg" {
		t.Errorf("expected format=mjpeg, got %q", env.Data.Format)
	}
	if env.Meta.Command != "camera stream" {
		t.Errorf("expected command='camera stream', got %q", env.Meta.Command)
	}
}

func TestStream_JSONError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubDriver{
		caps:      driver.Capabilities{CameraStream: false},
		streamErr: nil,
	}
	deps := makeDeps(t, dir, kc, drv)
	out, _ := runCmd(t, deps, "myprinter", "--output", "json")
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
		t.Error("expected ok=false")
	}
	if env.Error.Code != "capability_unsupported" {
		t.Errorf("expected code=capability_unsupported, got %q", env.Error.Code)
	}
	if env.Meta.Command != "camera stream" {
		t.Errorf("expected command='camera stream', got %q", env.Meta.Command)
	}
}

func TestStream_HTTPServesStream(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)

	streamData := "test-stream-bytes"
	drv := &stubDriver{
		caps: driver.Capabilities{CameraStream: true},
		streamRes: &driver.CameraStreamResult{
			Format:       driver.CameraFormatMJPEG,
			Stream:       io.NopCloser(strings.NewReader(streamData)),
			Capabilities: driver.Capabilities{CameraStream: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)

	// Find a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	// Run command in background with a short timeout.
	root := testRoot(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"camera", "stream", "myprinter", "--port", portStr(port), "--timeout", "5s"})

	done := make(chan error, 1)
	go func() {
		done <- root.Execute()
	}()

	// Wait for server to be ready.
	addr := "127.0.0.1:" + portStr(port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, connErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if connErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Fetch the stream.
	resp, err := http.Get("http://" + addr + "/stream")
	if err != nil {
		t.Fatalf("HTTP GET /stream failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "multipart/x-mixed-replace") {
		t.Errorf("expected MJPEG content type, got %q", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != streamData {
		t.Errorf("expected %q, got %q", streamData, string(body))
	}
}

func TestStream_HTTP404ForOtherPaths(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)

	drv := &stubDriver{
		caps: driver.Capabilities{CameraStream: true},
		streamRes: &driver.CameraStreamResult{
			Format:       driver.CameraFormatMJPEG,
			Stream:       io.NopCloser(&blockingReader{}),
			Capabilities: driver.Capabilities{CameraStream: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	root := testRoot(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"camera", "stream", "myprinter", "--port", portStr(port), "--timeout", "5s"})

	done := make(chan error, 1)
	go func() {
		done <- root.Execute()
	}()

	addr := "127.0.0.1:" + portStr(port)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, connErr := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if connErr == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	resp, err := http.Get("http://" + addr + "/other")
	if err != nil {
		t.Fatalf("HTTP GET /other failed: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for /other, got %d", resp.StatusCode)
	}
}

func TestStream_DoesNotLeakAccessCode(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := defaultDriver()
	deps := makeDeps(t, dir, kc, drv)
	port := freePort(t)
	out, _ := runCmd(t, deps, "myprinter", "--timeout", "1ms", "--port", portStr(port))
	if strings.Contains(out, "testcode") {
		t.Error("output contains access code — secret leak!")
	}
}

// blockingReader blocks forever on Read, useful for keeping the server alive during tests.
type blockingReader struct{}

func (b *blockingReader) Read(_ []byte) (int, error) {
	select {} //nolint:gosimple // intentional infinite block for test
}

func portStr(port int) string {
	return fmt.Sprintf("%d", port)
}

// freePort finds an available TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

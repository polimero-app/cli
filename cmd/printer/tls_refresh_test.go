package printer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// stubRefreshDriver satisfies driver.Driver for tls refresh command tests.
type stubRefreshDriver struct {
	fp   string
	err  error
	caps driver.Capabilities
}

func (s *stubRefreshDriver) Name() string                      { return "bambu-lan" }
func (s *stubRefreshDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubRefreshDriver) ConnectCheck(_ context.Context, _, _, _ string, _ bool, _ time.Duration) (string, error) {
	return "", nil
}
func (s *stubRefreshDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, nil
}
func (s *stubRefreshDriver) CaptureFingerprint(_ context.Context, _, _ string) (string, error) {
	return s.fp, s.err
}

func defaultRefreshDriver() *stubRefreshDriver {
	return &stubRefreshDriver{
		fp:   "sha256:deadbeef",
		caps: driver.Capabilities{TLSRefresh: true},
	}
}

func tlsRefreshDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver, prompter tty.Prompter) printer.TlsRefreshDeps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return printer.TlsRefreshDeps{
		KC: kc,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" && drv != nil {
				return drv, true
			}
			return nil, false
		},
		Prompter: prompter,
	}
}

func testRootForTlsRefresh(deps printer.TlsRefreshDeps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	sub := &cobra.Command{Use: "printer"}
	tlsSub := &cobra.Command{Use: "tls"}
	tlsSub.AddCommand(printer.TlsRefreshCommandWithDeps(deps))
	sub.AddCommand(tlsSub)
	root.AddCommand(sub)
	return root
}

func runTlsRefreshCmd(t *testing.T, deps printer.TlsRefreshDeps, args ...string) (string, error) {
	t.Helper()
	root := testRootForTlsRefresh(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "tls", "refresh"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Tests ---

func TestTlsRefresh_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})
	out, err := runTlsRefreshCmd(t, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected help output")
	}
}

func TestTlsRefresh_TooManyArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})
	_, err := runTlsRefreshCmd(t, deps, "one", "two")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestTlsRefresh_InvalidProfileName_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})
	_, err := runTlsRefreshCmd(t, deps, "_invalid", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid profile name, got %v", err)
	}
}

func TestTlsRefresh_ProfileNotFound_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})
	_, err := runTlsRefreshCmd(t, deps, "nonexistent", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for missing profile, got %v", err)
	}
}

func TestTlsRefresh_NonInteractive_NoYes_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})
	_, err := runTlsRefreshCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for non-interactive without --yes, got %v", err)
	}
}

func TestTlsRefresh_ConfirmationDeclined_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: true, Lines: []string{"no"}})
	_, err := runTlsRefreshCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 when confirmation declined, got %v", err)
	}
}

func TestTlsRefresh_HappyPath_SecureProfile(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := defaultRefreshDriver()
	deps := tlsRefreshDeps(t, dir, kc, drv, &tty.Mock{Terminal: false})

	out, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("TLS certificate re-pinned: myprinter")) {
		t.Errorf("expected success message in output:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("sha256:deadbeef")) {
		t.Errorf("expected fingerprint in output:\n%s", out)
	}

	// Fingerprint must be stored in keychain
	fp, kcErr := kc.Get("polimero", "bambu-lan:myprinter:tls-fingerprint")
	if kcErr != nil {
		t.Fatalf("fingerprint not in keychain: %v", kcErr)
	}
	if fp != "sha256:deadbeef" {
		t.Errorf("fingerprint = %q, want sha256:deadbeef", fp)
	}

	// Profile must reflect insecure=false
	cfg, _ := config.Open(dir)
	p, ok := cfg.GetProfile("myprinter")
	if !ok {
		t.Fatal("profile not found after refresh")
	}
	if p.Insecure {
		t.Error("profile should not be insecure after secure refresh")
	}
}

func TestTlsRefresh_UpdatesTimestamp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)

	before := time.Now().Add(-time.Second)
	deps := tlsRefreshDeps(t, dir, kc, &stubRefreshDriver{fp: "sha256:new", caps: driver.Capabilities{TLSRefresh: true}}, &tty.Mock{Terminal: false})
	_, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, cfgErr := config.Open(dir)
	if cfgErr != nil {
		t.Fatalf("config.Open: %v", cfgErr)
	}
	p, ok := cfg.GetProfile("myprinter")
	if !ok {
		t.Fatal("profile not found after refresh")
	}
	if !p.Updated.After(before) {
		t.Errorf("profile.Updated = %v, want after %v", p.Updated, before)
	}
	if p.Insecure {
		t.Error("profile.Insecure should be false after secure refresh")
	}
}

func TestTlsRefresh_InsecureToSecure(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true) // starts insecure
	drv := defaultRefreshDriver()
	deps := tlsRefreshDeps(t, dir, kc, drv, &tty.Mock{Terminal: false})

	_, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Profile must now be secure
	cfg, _ := config.Open(dir)
	p, ok := cfg.GetProfile("myprinter")
	if !ok {
		t.Fatal("profile not found after refresh")
	}
	if p.Insecure {
		t.Error("profile should be secure after tls refresh")
	}

	// Fingerprint must be in keychain
	fp, kcErr := kc.Get("polimero", "bambu-lan:myprinter:tls-fingerprint")
	if kcErr != nil {
		t.Fatalf("fingerprint not in keychain: %v", kcErr)
	}
	if fp != "sha256:deadbeef" {
		t.Errorf("fingerprint = %q, want sha256:deadbeef", fp)
	}
}

func TestTlsRefresh_SecureToInsecure(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false) // starts secure, has fingerprint
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})

	out, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes", "--insecure")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("TLS certificate verification disabled: myprinter")) {
		t.Errorf("expected insecure success message in output:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("Warning: TLS verification is disabled for this profile.")) {
		t.Errorf("expected warning in output:\n%s", out)
	}

	// Fingerprint must be deleted from keychain
	_, kcErr := kc.Get("polimero", "bambu-lan:myprinter:tls-fingerprint")
	if !errors.Is(kcErr, keychain.ErrNotFound) {
		t.Errorf("expected fingerprint to be deleted from keychain, got: %v", kcErr)
	}

	// Profile must reflect insecure=true
	cfg, _ := config.Open(dir)
	p, ok := cfg.GetProfile("myprinter")
	if !ok {
		t.Fatal("profile not found after refresh")
	}
	if !p.Insecure {
		t.Error("profile should be insecure after --insecure flag")
	}
}

func TestTlsRefresh_DoesNotTouchAccessCode(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})

	_, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Access code must still be in keychain
	ac, kcErr := kc.Get("polimero", "bambu-lan:myprinter:access-code")
	if kcErr != nil {
		t.Fatalf("access code not in keychain after tls refresh: %v", kcErr)
	}
	if ac != "testcode" {
		t.Errorf("access code = %q, want testcode", ac)
	}
}

func TestTlsRefresh_ConnectionError_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	drv := &stubRefreshDriver{
		caps: driver.Capabilities{TLSRefresh: true},
		err:  apperr.New(4, "connection refused"),
	}
	deps := tlsRefreshDeps(t, dir, kc, drv, &tty.Mock{Terminal: false})

	_, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestTlsRefresh_CapabilityUnsupported_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	noCapDrv := &stubRefreshDriver{caps: driver.Capabilities{Status: true, TLSRefresh: false}}
	deps := tlsRefreshDeps(t, dir, kc, noCapDrv, &tty.Mock{Terminal: false})
	_, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit 5 for unsupported capability, got %v", err)
	}
}

func TestTlsRefresh_JSON_ErrorEnvelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := tlsRefreshDeps(t, dir, kc,
		&stubRefreshDriver{caps: driver.Capabilities{Status: true, TLSRefresh: true}, err: apperr.New(4, "TLS connect failed: connection refused")},
		&tty.Mock{Terminal: false},
	)
	out, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes", "--output", "json")
	// The command returns an apperr exit error to caller; JSON was already written to out
	_ = err
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	errMap, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or wrong type: %v", env["error"])
	}
	if errMap["code"] != "connection_failed" {
		t.Errorf("error.code = %v, want connection_failed", errMap["code"])
	}
	details, ok := errMap["details"].(map[string]any)
	if !ok {
		t.Fatalf("error.details missing or wrong type: %v", errMap["details"])
	}
	if details["profile"] != "myprinter" {
		t.Errorf("error.details.profile = %v, want myprinter", details["profile"])
	}
	meta := env["meta"].(map[string]any)
	if meta["command"] != "printer tls refresh" {
		t.Errorf("meta.command = %v, want printer tls refresh", meta["command"])
	}
	if meta["durationMs"] != nil {
		t.Errorf("meta.durationMs should be absent in error response, got %v", meta["durationMs"])
	}
}

func TestTlsRefresh_JSON_SuccessEnvelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})

	out, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want map", env["data"])
	}
	if data["profile"] != "myprinter" {
		t.Errorf("data.profile = %v, want myprinter", data["profile"])
	}
	if data["fingerprint"] != "sha256:deadbeef" {
		t.Errorf("data.fingerprint = %v, want sha256:deadbeef", data["fingerprint"])
	}
	if data["insecure"] != false {
		t.Errorf("data.insecure = %v, want false", data["insecure"])
	}
	meta, ok := env["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", env["meta"])
	}
	if meta["command"] != "printer tls refresh" {
		t.Errorf("meta.command = %v, want printer tls refresh", meta["command"])
	}
	if meta["durationMs"] == nil {
		t.Error("meta.durationMs must be present for successful secure tls refresh")
	}
}

func TestTlsRefresh_JSON_InsecureEnvelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	deps := tlsRefreshDeps(t, dir, kc, defaultRefreshDriver(), &tty.Mock{Terminal: false})

	out, err := runTlsRefreshCmd(t, deps, "myprinter", "--yes", "--insecure", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want map", env["data"])
	}
	if data["fingerprint"] != nil {
		t.Errorf("data.fingerprint = %v, want null", data["fingerprint"])
	}
	if data["insecure"] != true {
		t.Errorf("data.insecure = %v, want true", data["insecure"])
	}
	meta, ok := env["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", env["meta"])
	}
	if meta["durationMs"] != nil {
		t.Errorf("meta.durationMs should be absent for insecure path, got %v", meta["durationMs"])
	}
}

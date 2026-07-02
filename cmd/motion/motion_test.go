package motion_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/motion"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubMotionDriver satisfies driver.Driver and driver.MotionDriver.
type stubMotionDriver struct {
	caps      driver.Capabilities
	statusRes *driver.StatusResult
	statusErr error
	homeRes   *driver.MotionResult
	homeErr   error
	jogRes    *driver.MotionResult
	jogErr    error
}

func (s *stubMotionDriver) Name() string                      { return "bambu-lan" }
func (s *stubMotionDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubMotionDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubMotionDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubMotionDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return s.statusRes, s.statusErr
}
func (s *stubMotionDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubMotionDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubMotionDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubMotionDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}
func (s *stubMotionDriver) MotionHome(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, _ []driver.Axis) (driver.MotionResult, error) {
	if s.homeErr != nil {
		return driver.MotionResult{}, s.homeErr
	}
	if s.homeRes != nil {
		return *s.homeRes, nil
	}
	return driver.MotionResult{State: driver.MotionStateAccepted, Capabilities: s.caps}, nil
}
func (s *stubMotionDriver) MotionJog(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, _ driver.JogDelta) (driver.MotionResult, error) {
	if s.jogErr != nil {
		return driver.MotionResult{}, s.jogErr
	}
	if s.jogRes != nil {
		return *s.jogRes, nil
	}
	return driver.MotionResult{State: driver.MotionStateAccepted, Capabilities: s.caps}, nil
}

func defaultMotionDriver() *stubMotionDriver {
	caps := driver.Capabilities{MotionControl: true}
	return &stubMotionDriver{
		caps: caps,
		statusRes: &driver.StatusResult{
			State:        "idle",
			Errors:       []driver.StatusError{},
			Warnings:     []driver.StatusWarning{},
			Capabilities: caps,
		},
	}
}

func seedProfile(t *testing.T, dir string, kc *keychain.Mock, name string) {
	t.Helper()
	now := time.Now().UTC()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.AddProfile(name, config.Profile{
		Driver:  "bambu-lan",
		Host:    "192.0.2.10",
		Serial:  "SN001",
		Timeout: "10s",
		Created: now,
		Updated: now,
	})
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":access-code", "testcode")
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":tls-fingerprint", testFingerprint)
}

func makeDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver, prompter tty.Prompter) motion.Deps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return motion.Deps{
		KC: kc,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" && drv != nil {
				return drv, true
			}
			return nil, false
		},
		Log:      slog.Default(),
		Prompter: prompter,
	}
}

func runCmd(t *testing.T, deps motion.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(motion.CommandWithDeps(deps))
	buf := &strings.Builder{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"motion"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- motion home tests ---

func TestMotionHome_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "home")
	if err != nil {
		t.Errorf("expected no error (help), got %v", err)
	}
	if !strings.Contains(out, "home <printer>") {
		t.Errorf("expected usage in help, got:\n%s", out)
	}
}

func TestMotionHome_AllAxes_HumanSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "home", "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Homing x, y, z") {
		t.Errorf("expected homing message, got:\n%s", out)
	}
	if !strings.Contains(out, "Homing command accepted") {
		t.Errorf("expected accepted message, got:\n%s", out)
	}
}

func TestMotionHome_SubsetAxes_HumanSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "home", "myprinter", "--axis", "x,z", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Homing x, z") {
		t.Errorf("expected 'Homing x, z', got:\n%s", out)
	}
}

func TestMotionHome_JSONSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "home", "myprinter", "--yes", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env)
	}
	data := env["data"].(map[string]any)
	if data["action"] != "home" {
		t.Errorf("expected action=home, got %v", data["action"])
	}
	if data["state"] != "accepted" {
		t.Errorf("expected state=accepted, got %v", data["state"])
	}
	axes, ok := data["axes"].([]any)
	if !ok || len(axes) != 3 {
		t.Errorf("expected 3 axes, got %v", data["axes"])
	}
}

func TestMotionHome_InvalidAxis_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "home", "myprinter", "--axis", "x,w", "--yes")
	if err == nil {
		t.Fatal("expected error for invalid axis")
	}
}

func TestMotionHome_InvalidPrinterState_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := defaultMotionDriver()
	drv.statusRes = &driver.StatusResult{
		State:        "printing",
		Errors:       []driver.StatusError{},
		Warnings:     []driver.StatusWarning{},
		Capabilities: drv.caps,
	}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "home", "myprinter", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "invalid_printer_state" {
		t.Errorf("expected invalid_printer_state, got %v", errObj["code"])
	}
}

func TestMotionHome_ConfirmationDeclined_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true, Lines: []string{"no"}})
	_, err := runCmd(t, deps, "home", "myprinter")
	if err == nil {
		t.Fatal("expected error when confirmation declined")
	}
}

func TestMotionHome_NonInteractive_WithoutYes_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: false})
	_, err := runCmd(t, deps, "home", "myprinter")
	if err == nil {
		t.Fatal("expected error in non-interactive mode")
	}
}

func TestMotionHome_UnsupportedCapability_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := defaultMotionDriver()
	drv.caps = driver.Capabilities{}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "home", "myprinter", "--yes")
	if err == nil {
		t.Fatal("expected capability error")
	}
	e, ok := err.(*apperr.ExitError)
	if !ok || e.Code != 5 {
		t.Errorf("expected exit code 5, got %v", err)
	}
}

// --- motion jog tests ---

func TestMotionJog_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog")
	if err != nil {
		t.Errorf("expected no error (help), got %v", err)
	}
	if !strings.Contains(out, "jog <printer>") {
		t.Errorf("expected usage in help, got:\n%s", out)
	}
}

func TestMotionJog_SingleAxis_HumanSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--x", "5", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Jogging") {
		t.Errorf("expected jogging message, got:\n%s", out)
	}
	if !strings.Contains(out, "Jog command accepted") {
		t.Errorf("expected accepted message, got:\n%s", out)
	}
}

func TestMotionJog_JSONSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--x", "5", "--yes", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env)
	}
	data := env["data"].(map[string]any)
	if data["action"] != "jog" {
		t.Errorf("expected action=jog, got %v", data["action"])
	}
	if data["state"] != "accepted" {
		t.Errorf("expected state=accepted, got %v", data["state"])
	}
}

func TestMotionJog_NoAxisFlag_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "jog", "myprinter", "--yes")
	if err == nil {
		t.Fatal("expected error when no axis flag given")
	}
}

func TestMotionJog_XTooLarge_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--x", "25", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for out-of-range jog")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected unsafe_value, got %v", errObj["code"])
	}
	details := errObj["details"].(map[string]any)
	if details["axis"] != "x" {
		t.Errorf("expected axis=x in details, got %v", details)
	}
}

func TestMotionJog_ZNegativeTooLarge_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--z", "-15", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for out-of-range jog")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected unsafe_value, got %v", errObj["code"])
	}
}

func TestMotionJog_XNaN_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--x", "NaN", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for NaN jog distance")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected unsafe_value, got %v", errObj["code"])
	}
}

func TestMotionJog_YInf_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--y", "Inf", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for Inf jog distance")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected unsafe_value, got %v", errObj["code"])
	}
}

func TestMotionJog_InvalidPrinterState_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := defaultMotionDriver()
	drv.statusRes = &driver.StatusResult{
		State:        "printing",
		Errors:       []driver.StatusError{},
		Warnings:     []driver.StatusWarning{},
		Capabilities: drv.caps,
	}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "jog", "myprinter", "--x", "5", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for invalid state")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "invalid_printer_state" {
		t.Errorf("expected invalid_printer_state, got %v", errObj["code"])
	}
}

func TestMotionJog_NonInteractive_WithoutYes_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultMotionDriver(), &tty.Mock{Terminal: false})
	_, err := runCmd(t, deps, "jog", "myprinter", "--x", "5")
	if err == nil {
		t.Fatal("expected error in non-interactive mode")
	}
}

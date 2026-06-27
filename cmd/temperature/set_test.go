package temperature_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/temperature"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubTempDriver satisfies driver.Driver and driver.TemperatureDriver.
type stubTempDriver struct {
	caps      driver.Capabilities
	statusRes *driver.StatusResult
	statusErr error
	tempRes   *driver.TemperatureResult
	tempErr   error
}

func (s *stubTempDriver) Name() string                      { return "bambu-lan" }
func (s *stubTempDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubTempDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubTempDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubTempDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return s.statusRes, s.statusErr
}
func (s *stubTempDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubTempDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubTempDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubTempDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}
func (s *stubTempDriver) TemperatureSet(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, _ driver.TemperatureTargets) (driver.TemperatureResult, error) {
	if s.tempErr != nil {
		return driver.TemperatureResult{}, s.tempErr
	}
	if s.tempRes != nil {
		return *s.tempRes, nil
	}
	return driver.TemperatureResult{Capabilities: s.caps}, nil
}

func defaultTempDriver() *stubTempDriver {
	caps := driver.Capabilities{TemperatureWrite: true}
	return &stubTempDriver{
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

func makeDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver, prompter tty.Prompter) temperature.Deps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return temperature.Deps{
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

func runCmd(t *testing.T, deps temperature.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(temperature.CommandWithDeps(deps))
	buf := &strings.Builder{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"temperature"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func TestTemperatureSet_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set")
	if err != nil {
		t.Errorf("expected no error (help), got %v", err)
	}
	if !strings.Contains(out, "set <printer>") {
		t.Errorf("expected usage in help, got:\n%s", out)
	}
}

func TestTemperatureSet_NozzleOnly_HumanSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	nozzle := 220.0
	drv := defaultTempDriver()
	drv.tempRes = &driver.TemperatureResult{
		Targets:      driver.TemperatureTargets{NozzleCelsius: &nozzle},
		Capabilities: drv.caps,
	}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true, Lines: []string{"yes"}})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Nozzle target set to 220.0") {
		t.Errorf("expected nozzle line in output, got:\n%s", out)
	}
}

func TestTemperatureSet_NozzleAndBed_HumanSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	nozzle, bed := 220.0, 60.0
	drv := defaultTempDriver()
	drv.tempRes = &driver.TemperatureResult{
		Targets:      driver.TemperatureTargets{NozzleCelsius: &nozzle, BedCelsius: &bed},
		Capabilities: drv.caps,
	}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220", "--bed", "60", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Nozzle target set to 220.0") {
		t.Errorf("expected nozzle line, got:\n%s", out)
	}
	if !strings.Contains(out, "Bed target set to 60.0") {
		t.Errorf("expected bed line, got:\n%s", out)
	}
}

func TestTemperatureSet_JSONSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	nozzle := 220.0
	drv := defaultTempDriver()
	drv.tempRes = &driver.TemperatureResult{
		Targets:      driver.TemperatureTargets{NozzleCelsius: &nozzle},
		Capabilities: drv.caps,
	}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220", "--yes", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if env["ok"] != true {
		t.Errorf("expected ok=true, got %v", env)
	}
	meta := env["meta"].(map[string]any)
	if meta["command"] != "temperature set" {
		t.Errorf("expected command=temperature set, got %v", meta["command"])
	}
}

func TestTemperatureSet_NoTargetFlag_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--yes")
	if err == nil {
		t.Fatal("expected error when no target flag given")
	}
	if !strings.Contains(out, "at least one") {
		t.Errorf("expected 'at least one' in output, got:\n%s", out)
	}
}

func TestTemperatureSet_NozzleTooHigh_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "350", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for out-of-range nozzle")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected code=unsafe_value, got %v", errObj["code"])
	}
	details := errObj["details"].(map[string]any)
	if details["target"] != "nozzle" {
		t.Errorf("expected target=nozzle in details, got %v", details)
	}
}

func TestTemperatureSet_BedTooHigh_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--bed", "150", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for out-of-range bed")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected code=unsafe_value, got %v", errObj["code"])
	}
}

func TestTemperatureSet_NozzleNegative_UnsafeValue(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "-1", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for negative nozzle")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "unsafe_value" {
		t.Errorf("expected code=unsafe_value, got %v", errObj["code"])
	}
}

func TestTemperatureSet_InvalidPrinterState_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := defaultTempDriver()
	drv.statusRes = &driver.StatusResult{
		State:        "printing",
		Errors:       []driver.StatusError{},
		Warnings:     []driver.StatusWarning{},
		Capabilities: drv.caps,
	}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error for invalid printer state")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "invalid_printer_state" {
		t.Errorf("expected code=invalid_printer_state, got %v", errObj["code"])
	}
	details := errObj["details"].(map[string]any)
	if details["currentState"] != "printing" {
		t.Errorf("expected currentState=printing, got %v", details)
	}
}

func TestTemperatureSet_ConfirmationDeclined_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true, Lines: []string{"no"}})
	_, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220")
	if err == nil {
		t.Fatal("expected error when confirmation declined")
	}
}

func TestTemperatureSet_NonInteractive_WithoutYes_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: false})
	_, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220")
	if err == nil {
		t.Fatal("expected error in non-interactive mode without --yes")
	}
}

func TestTemperatureSet_UnsupportedCapability_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := defaultTempDriver()
	drv.caps = driver.Capabilities{} // no TemperatureWrite
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220", "--yes")
	if err == nil {
		t.Fatal("expected capability error")
	}
	var exitErr *apperr.ExitError
	if ok := func() bool {
		e, ok := err.(*apperr.ExitError)
		if ok && e.Code == 5 {
			return true
		}
		return false
	}(); !ok {
		_ = exitErr // avoid "unused" warning
		t.Errorf("expected exit code 5, got %v", err)
	}
}

func TestTemperatureSet_SkipsConfirmationWithYes(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	// No prompter lines needed when --yes is set
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "set", "myprinter", "--nozzle", "220", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
}

func TestTemperatureSet_ProfileNotFound_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultTempDriver(), &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "set", "noprinter", "--nozzle", "220", "--yes")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
}

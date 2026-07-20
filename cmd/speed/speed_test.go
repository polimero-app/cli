package speed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/speed"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type stubSpeedDriver struct {
	caps        driver.Capabilities
	statusRes   *driver.StatusResult
	statusErr   error
	speedRes    *driver.SpeedControlResult
	speedErr    error
	speedCalls  int
	lastProfile string
}

func (s *stubSpeedDriver) Name() string                      { return "bambu-lan" }
func (s *stubSpeedDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubSpeedDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubSpeedDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubSpeedDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return s.statusRes, s.statusErr
}
func (s *stubSpeedDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubSpeedDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubSpeedDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubSpeedDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}
func (s *stubSpeedDriver) SpeedSet(ctx context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, profile string) (driver.SpeedControlResult, error) {
	if err := ctx.Err(); err != nil {
		return driver.SpeedControlResult{}, err
	}
	s.speedCalls++
	s.lastProfile = profile
	if s.speedErr != nil {
		return driver.SpeedControlResult{}, s.speedErr
	}
	if s.speedRes != nil {
		return *s.speedRes, nil
	}
	return driver.SpeedControlResult{
		SpeedProfile: profile,
		Capabilities: s.caps,
	}, nil
}

type stubNoSpeedDriver struct {
	caps driver.Capabilities
}

func (s *stubNoSpeedDriver) Name() string                      { return "bambu-lan" }
func (s *stubNoSpeedDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubNoSpeedDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubNoSpeedDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubNoSpeedDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return &driver.StatusResult{
		State:        "printing",
		Errors:       []driver.StatusError{},
		Warnings:     []driver.StatusWarning{},
		Capabilities: s.caps,
	}, nil
}
func (s *stubNoSpeedDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubNoSpeedDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubNoSpeedDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubNoSpeedDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}

func defaultSpeedDriver() *stubSpeedDriver {
	caps := driver.Capabilities{SpeedControl: true}
	return &stubSpeedDriver{
		caps: caps,
		statusRes: &driver.StatusResult{
			State:        "printing",
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
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":access-code", "secret123")
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":tls-fingerprint", testFingerprint)
}

func setupTest(t *testing.T) (string, speed.Deps, *keychain.Mock, *tty.Mock, *stubSpeedDriver) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)

	kc := keychain.NewMock()
	prompter := &tty.Mock{}
	drv := defaultSpeedDriver()

	deps := speed.Deps{
		KC: kc,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" {
				return drv, true
			}
			return nil, false
		},
		Log:      slog.Default(),
		Prompter: prompter,
	}

	return dir, deps, kc, prompter, drv
}

func executeCommand(root *cobra.Command, args ...string) (string, string, error) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestSetCommand_MissingArguments_ExitsCode2(t *testing.T) {
	_, deps, _, _, _ := setupTest(t)
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	_, errOut, err := executeCommand(&root, "speed", "set")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "profile name and speed profile are required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}

	_, errOut, err = executeCommand(&root, "speed", "set", "printer1")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "speed profile is required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}
}

func TestSetCommand_TooManyArguments_ExitsCode2(t *testing.T) {
	_, deps, _, _, _ := setupTest(t)
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	_, errOut, err := executeCommand(&root, "speed", "set", "printer1", "sport", "extra")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "expected exactly two arguments") {
		t.Errorf("unexpected usage error: %s", errOut)
	}
}

func TestSetCommand_MalformedArguments_ExitsCode2(t *testing.T) {
	_, deps, _, _, _ := setupTest(t)
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	_, errOut, err := executeCommand(&root, "speed", "set", "printer1", "Sport")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid speed profile syntax") {
		t.Errorf("unexpected error output: %s", errOut)
	}
}

func TestSetCommand_Success_HumanOutput(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	out, _, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if drv.speedCalls != 1 {
		t.Errorf("expected 1 speed call, got %d", drv.speedCalls)
	}
	if drv.lastProfile != "sport" {
		t.Errorf("expected profile sport, got %q", drv.lastProfile)
	}

	expected := "Printer: garage\nSpeed profile set to sport.\n"
	if out != expected {
		t.Errorf("unexpected output:\nwant: %q\ngot:  %q", expected, out)
	}
}

func TestSetCommand_Success_JSONOutput(t *testing.T) {
	dir, deps, _, _, _ := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "json", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	out, _, err := executeCommand(&root, "speed", "set", "garage", "silent", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Profile      string `json:"profile"`
			Driver       string `json:"driver"`
			SpeedProfile string `json:"speedProfile"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("cannot unmarshal output: %v", err)
	}
	if !envelope.OK {
		t.Errorf("expected envelope.ok to be true")
	}
	if envelope.Data.Profile != "garage" || envelope.Data.Driver != "bambu-lan" || envelope.Data.SpeedProfile != "silent" {
		t.Errorf("unexpected data fields in json: %+v", envelope.Data)
	}
}

func TestSetCommand_InteractiveConfirmation_Declined_ExitsCode2(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	prompter.Terminal = true
	prompter.Lines = []string{"no"}

	_, errOut, err := executeCommand(&root, "speed", "set", "garage", "sport")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "confirmation declined") {
		t.Errorf("unexpected error: %s", errOut)
	}
	if drv.speedCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_InteractiveConfirmation_Accepted(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	prompter.Terminal = true
	prompter.Lines = []string{"yes"}

	out, _, err := executeCommand(&root, "speed", "set", "garage", "ludicrous")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if drv.speedCalls != 1 {
		t.Errorf("expected driver call")
	}
	if !strings.Contains(out, "Speed profile set to ludicrous.") {
		t.Errorf("unexpected success output: %s", out)
	}
}

func TestSetCommand_NonInteractiveWithoutYes_ExitsCode2(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	prompter.Terminal = false
	_, errOut, err := executeCommand(&root, "speed", "set", "garage", "sport")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "non-interactive mode requires --yes") {
		t.Errorf("unexpected error: %s", errOut)
	}
	if drv.speedCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_InvalidPrinterState_ExitsCode2(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	drv.statusRes.State = "idle"

	_, errOut, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid_printer_state") && !strings.Contains(errOut, "cannot perform action: printer is idle") {
		t.Errorf("unexpected error: %s", errOut)
	}
}

func TestSetCommand_AllowedPausedState_RunsSuccessfully(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	drv.statusRes.State = "paused"

	out, _, err := executeCommand(&root, "speed", "set", "garage", "standard", "--yes")
	if err != nil {
		t.Fatalf("unexpected error on state paused: %v", err)
	}
	if drv.speedCalls != 1 {
		t.Errorf("expected speed call in paused state")
	}
	if !strings.Contains(out, "Speed profile set to standard.") {
		t.Errorf("unexpected success output: %s", out)
	}
}

func TestSetCommand_UnsupportedCapability_ExitsCode5(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	drv.caps = driver.Capabilities{}
	_, _, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Fatalf("expected exit code 5, got %v", err)
	}
	if drv.speedCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_StatusFailure_ExitsCode4(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	drv.statusErr = apperr.New(4, "status timed out")
	_, _, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Fatalf("expected exit code 4, got %v", err)
	}
	if drv.speedCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_StatusNilResult_ExitsCode1(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	drv.statusRes = nil
	_, _, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
	if drv.speedCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_ProfileNotFound_ExitsCode2(t *testing.T) {
	_, deps, _, _, _ := setupTest(t)
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	_, _, err := executeCommand(&root, "speed", "set", "missing", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
}

func TestSetCommand_ActionMismatch_ExitsCode1AndCode(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "json", "")
	root.AddCommand(speed.CommandWithDeps(deps))

	drv.speedRes = &driver.SpeedControlResult{
		SpeedProfile: "standard",
		Capabilities: drv.caps,
	}
	out, _, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &envelope); jsonErr != nil {
		t.Fatalf("invalid JSON output: %v", jsonErr)
	}
	if envelope.Error.Code != "speed_action_failed" {
		t.Errorf("expected speed_action_failed, got %q", envelope.Error.Code)
	}
}

func TestSetCommand_DriverWithoutSpeedInterface_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "garage")
	prompter := &tty.Mock{}
	drv := &stubNoSpeedDriver{caps: driver.Capabilities{SpeedControl: true}}

	deps := speed.Deps{
		KC: kc,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" {
				return drv, true
			}
			return nil, false
		},
		Log:      slog.Default(),
		Prompter: prompter,
	}

	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(speed.CommandWithDeps(deps))
	_, _, err := executeCommand(&root, "speed", "set", "garage", "sport", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Fatalf("expected exit code 5, got %v", err)
	}
}

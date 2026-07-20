package fans_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/fans"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type stubFanDriver struct {
	caps        driver.Capabilities
	statusRes   *driver.StatusResult
	statusErr   error
	fanRes      *driver.FanControlResult
	fanErr      error
	statusCalls int
	fanCalls    int
	lastTarget  driver.FanTarget
}

func (s *stubFanDriver) Name() string                      { return "bambu-lan" }
func (s *stubFanDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubFanDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubFanDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubFanDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	s.statusCalls++
	return s.statusRes, s.statusErr
}
func (s *stubFanDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubFanDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubFanDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubFanDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}
func (s *stubFanDriver) FanSet(ctx context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, target driver.FanTarget) (driver.FanControlResult, error) {
	if err := ctx.Err(); err != nil {
		return driver.FanControlResult{}, err
	}
	s.fanCalls++
	s.lastTarget = target
	if s.fanErr != nil {
		return driver.FanControlResult{}, s.fanErr
	}
	if s.fanRes != nil {
		return *s.fanRes, nil
	}
	return driver.FanControlResult{
		Fan:          target.Fan,
		SpeedPercent: target.SpeedPercent,
		Capabilities: s.caps,
	}, nil
}

func defaultFanDriver() *stubFanDriver {
	caps := driver.Capabilities{FanControl: true}
	return &stubFanDriver{
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
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":access-code", "secret123")
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":tls-fingerprint", testFingerprint)
}

func setupTest(t *testing.T) (string, fans.Deps, *keychain.Mock, *tty.Mock, *stubFanDriver) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)

	kc := keychain.NewMock()
	prompter := &tty.Mock{}
	drv := defaultFanDriver()

	deps := fans.Deps{
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
	root.AddCommand(fans.CommandWithDeps(deps))

	// No arguments
	_, errOut, err := executeCommand(&root, "fans", "set")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "profile name, fan name, and speed percentage are required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}

	// Only printer
	_, errOut, err = executeCommand(&root, "fans", "set", "printer1")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "fan name and speed percentage are required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}

	// Only printer and fan
	_, errOut, err = executeCommand(&root, "fans", "set", "printer1", "part-cooling")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "speed percentage is required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}
}

func TestSetCommand_MalformedArguments_ExitsCode2(t *testing.T) {
	_, deps, _, _, _ := setupTest(t)
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(fans.CommandWithDeps(deps))

	// Malformed fan syntax
	_, errOut, err := executeCommand(&root, "fans", "set", "printer1", "part-cooling!", "50")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid fan name syntax") {
		t.Errorf("unexpected error output: %s", errOut)
	}

	// Malformed speed percent syntax
	_, errOut, err = executeCommand(&root, "fans", "set", "printer1", "part-cooling", "50.5")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid speed percent") {
		t.Errorf("unexpected error output: %s", errOut)
	}

	// Speed percent out of range
	_, errOut, err = executeCommand(&root, "fans", "set", "printer1", "part-cooling", "120")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "fan speed out of range") {
		t.Errorf("unexpected error output: %s", errOut)
	}
}

func TestSetCommand_Success_HumanOutput(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(fans.CommandWithDeps(deps))

	out, _, err := executeCommand(&root, "fans", "set", "garage", "part-cooling", "60", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if drv.fanCalls != 1 {
		t.Errorf("expected 1 fan call, got %d", drv.fanCalls)
	}
	if drv.lastTarget.Fan != "partCooling" {
		t.Errorf("expected normalized fan 'partCooling', got %q", drv.lastTarget.Fan)
	}
	if drv.lastTarget.SpeedPercent != 60 {
		t.Errorf("expected speed 60, got %d", drv.lastTarget.SpeedPercent)
	}

	expected := "Printer: garage\nPart cooling fan set to 60%.\n"
	if out != expected {
		t.Errorf("unexpected output:\nwant: %q\ngot:  %q", expected, out)
	}
}

func TestSetCommand_Success_JSONOutput(t *testing.T) {
	dir, deps, _, _, _ := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "json", "")
	root.AddCommand(fans.CommandWithDeps(deps))

	out, _, err := executeCommand(&root, "fans", "set", "garage", "aux", "40", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Profile      string `json:"profile"`
			Driver       string `json:"driver"`
			Fan          string `json:"fan"`
			SpeedPercent int    `json:"speedPercent"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("cannot unmarshal output: %v", err)
	}
	if !envelope.OK {
		t.Errorf("expected envelope.ok to be true")
	}
	if envelope.Data.Profile != "garage" || envelope.Data.Driver != "bambu-lan" || envelope.Data.Fan != "auxiliary" || envelope.Data.SpeedPercent != 40 {
		t.Errorf("unexpected data fields in json: %+v", envelope.Data)
	}
}

func TestSetCommand_InteractiveConfirmation_Declined_ExitsCode2(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(fans.CommandWithDeps(deps))

	prompter.Terminal = true
	prompter.Lines = []string{"no"}

	_, errOut, err := executeCommand(&root, "fans", "set", "garage", "chamber", "50")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "confirmation declined") {
		t.Errorf("unexpected error: %s", errOut)
	}
	if drv.fanCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_InteractiveConfirmation_Accepted(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(fans.CommandWithDeps(deps))

	prompter.Terminal = true
	prompter.Lines = []string{"yes"}

	out, _, err := executeCommand(&root, "fans", "set", "garage", "chamber", "50")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if drv.fanCalls != 1 {
		t.Errorf("expected driver call")
	}
	if !strings.Contains(out, "Chamber fan set to 50%") {
		t.Errorf("unexpected success output: %s", out)
	}
}

func TestSetCommand_InvalidPrinterState_ExitsCode2(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(fans.CommandWithDeps(deps))

	drv.statusRes.State = "offline"

	_, errOut, err := executeCommand(&root, "fans", "set", "garage", "part-cooling", "50", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid_printer_state") && !strings.Contains(errOut, "cannot perform action: printer is offline") {
		t.Errorf("unexpected error: %s", errOut)
	}
}

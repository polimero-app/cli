package lights_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/lights"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type stubLightDriver struct {
	caps        driver.Capabilities
	statusRes   *driver.StatusResult
	statusErr   error
	lightRes    *driver.LightControlResult
	lightErr    error
	statusCalls int
	lightCalls  int
	lastTarget  driver.LightTarget
}

func (s *stubLightDriver) Name() string                      { return "bambu-lan" }
func (s *stubLightDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubLightDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubLightDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubLightDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	s.statusCalls++
	return s.statusRes, s.statusErr
}
func (s *stubLightDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubLightDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubLightDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubLightDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}
func (s *stubLightDriver) LightSet(ctx context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, target driver.LightTarget) (driver.LightControlResult, error) {
	if err := ctx.Err(); err != nil {
		return driver.LightControlResult{}, err
	}
	s.lightCalls++
	s.lastTarget = target
	if s.lightErr != nil {
		return driver.LightControlResult{}, s.lightErr
	}
	if s.lightRes != nil {
		return *s.lightRes, nil
	}
	return driver.LightControlResult{
		Light:        target.Light,
		State:        target.State,
		Capabilities: s.caps,
	}, nil
}

func defaultLightDriver() *stubLightDriver {
	caps := driver.Capabilities{LightControl: true}
	return &stubLightDriver{
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

func setupTest(t *testing.T) (string, lights.Deps, *keychain.Mock, *tty.Mock, *stubLightDriver) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)

	kc := keychain.NewMock()
	prompter := &tty.Mock{}
	drv := defaultLightDriver()

	deps := lights.Deps{
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
	root.AddCommand(lights.CommandWithDeps(deps))

	// No arguments
	_, errOut, err := executeCommand(&root, "lights", "set")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "profile name, light name, and state are required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}

	// Only printer
	_, errOut, err = executeCommand(&root, "lights", "set", "printer1")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "light name and state are required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}

	// Only printer and light
	_, errOut, err = executeCommand(&root, "lights", "set", "printer1", "chamber")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "state is required") {
		t.Errorf("unexpected usage error: %s", errOut)
	}
}

func TestSetCommand_MalformedArguments_ExitsCode2(t *testing.T) {
	_, deps, _, _, _ := setupTest(t)
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	// Malformed light syntax
	_, errOut, err := executeCommand(&root, "lights", "set", "printer1", "chamber!", "on")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid light name syntax") {
		t.Errorf("unexpected error output: %s", errOut)
	}

	// Malformed state syntax
	_, errOut, err = executeCommand(&root, "lights", "set", "printer1", "chamber", "ON")
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid light state") {
		t.Errorf("unexpected error output: %s", errOut)
	}
}

func TestSetCommand_Success_HumanOutput(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	out, _, err := executeCommand(&root, "lights", "set", "garage", "chamber-light", "on", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if drv.lightCalls != 1 {
		t.Errorf("expected 1 light call, got %d", drv.lightCalls)
	}
	if drv.lastTarget.Light != "chamber" {
		t.Errorf("expected normalized light 'chamber', got %q", drv.lastTarget.Light)
	}
	if drv.lastTarget.State != driver.LightStateOn {
		t.Errorf("expected state 'on', got %q", drv.lastTarget.State)
	}

	expected := "Printer: garage\nChamber light set to on.\n"
	if out != expected {
		t.Errorf("unexpected output:\nwant: %q\ngot:  %q", expected, out)
	}
}

func TestSetCommand_Success_JSONOutput(t *testing.T) {
	dir, deps, _, _, _ := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "json", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	out, _, err := executeCommand(&root, "lights", "set", "garage", "chamber_light", "off", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			Profile string            `json:"profile"`
			Driver  string            `json:"driver"`
			Light   string            `json:"light"`
			State   driver.LightState `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("cannot unmarshal output: %v", err)
	}
	if !envelope.OK {
		t.Errorf("expected envelope.ok to be true")
	}
	if envelope.Data.Profile != "garage" || envelope.Data.Driver != "bambu-lan" || envelope.Data.Light != "chamber" || envelope.Data.State != driver.LightStateOff {
		t.Errorf("unexpected data fields in json: %+v", envelope.Data)
	}
}

func TestSetCommand_InteractiveConfirmation_Declined_ExitsCode2(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	prompter.Terminal = true
	prompter.Lines = []string{"no"}

	_, errOut, err := executeCommand(&root, "lights", "set", "garage", "chamber", "on")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "confirmation declined") {
		t.Errorf("unexpected error: %s", errOut)
	}
	if drv.lightCalls != 0 {
		t.Errorf("should not have called driver")
	}
}

func TestSetCommand_InteractiveConfirmation_Accepted(t *testing.T) {
	dir, deps, _, prompter, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	prompter.Terminal = true
	prompter.Lines = []string{"yes"}

	out, _, err := executeCommand(&root, "lights", "set", "garage", "chamber", "on")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if drv.lightCalls != 1 {
		t.Errorf("expected driver call")
	}
	if !strings.Contains(out, "Chamber light set to on.") {
		t.Errorf("unexpected success output: %s", out)
	}
}

func TestSetCommand_InvalidPrinterState_ExitsCode2(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	drv.statusRes.State = "offline"

	_, errOut, err := executeCommand(&root, "lights", "set", "garage", "chamber", "on", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errOut, "invalid_printer_state") && !strings.Contains(errOut, "cannot perform action: printer is offline") {
		t.Errorf("unexpected error: %s", errOut)
	}
}

func TestSetCommand_AllowedStateError_RunsSuccessfully(t *testing.T) {
	dir, deps, _, _, drv := setupTest(t)
	seedProfile(t, dir, deps.KC.(*keychain.Mock), "garage")
	root := cobra.Command{}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(lights.CommandWithDeps(deps))

	// Precondition allowed states: idle, printing, paused, error. Let's set to "error".
	drv.statusRes.State = "error"

	out, _, err := executeCommand(&root, "lights", "set", "garage", "chamber", "off", "--yes")
	if err != nil {
		t.Fatalf("unexpected error on state error: %v", err)
	}
	if drv.lightCalls != 1 {
		t.Errorf("expected light call in error state")
	}
	if !strings.Contains(out, "Chamber light set to off.") {
		t.Errorf("unexpected success output: %s", out)
	}
}

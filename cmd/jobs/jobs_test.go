package jobs_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/jobs"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubJobDriver satisfies driver.Driver and driver.JobDriver.
type stubJobDriver struct {
	caps      driver.Capabilities
	statusRes *driver.StatusResult
	statusErr error
	startRes  *driver.JobActionResult
	startErr  error
	pauseRes  *driver.JobActionResult
	pauseErr  error
	resumeRes *driver.JobActionResult
	resumeErr error
	cancelRes *driver.JobActionResult
	cancelErr error
}

func (s *stubJobDriver) Name() string                      { return "bambu-lan" }
func (s *stubJobDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubJobDriver) ValidateProfile(_ driver.ProfileInput) error {
	return nil
}
func (s *stubJobDriver) ConnectCheck(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle) (string, error) {
	return "", nil
}
func (s *stubJobDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return s.statusRes, s.statusErr
}
func (s *stubJobDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubJobDriver) CameraSnapshot(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraSnapshotResult, error) {
	return nil, apperr.New(5, "unsupported")
}
func (s *stubJobDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}
func (s *stubJobDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}
func (s *stubJobDriver) JobStart(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger, _ string, _ driver.JobStartOptions) (driver.JobActionResult, error) {
	if s.startErr != nil {
		return driver.JobActionResult{}, s.startErr
	}
	if s.startRes != nil {
		return *s.startRes, nil
	}
	return driver.JobActionResult{State: "printing", Capabilities: s.caps}, nil
}
func (s *stubJobDriver) JobPause(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	if s.pauseErr != nil {
		return driver.JobActionResult{}, s.pauseErr
	}
	if s.pauseRes != nil {
		return *s.pauseRes, nil
	}
	return driver.JobActionResult{State: "paused", Capabilities: s.caps}, nil
}
func (s *stubJobDriver) JobResume(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	if s.resumeErr != nil {
		return driver.JobActionResult{}, s.resumeErr
	}
	if s.resumeRes != nil {
		return *s.resumeRes, nil
	}
	return driver.JobActionResult{State: "printing", Capabilities: s.caps}, nil
}
func (s *stubJobDriver) JobCancel(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	if s.cancelErr != nil {
		return driver.JobActionResult{}, s.cancelErr
	}
	if s.cancelRes != nil {
		return *s.cancelRes, nil
	}
	return driver.JobActionResult{State: "idle", Capabilities: s.caps}, nil
}

func idleDriver() *stubJobDriver {
	caps := driver.Capabilities{JobStart: true, JobPause: true, JobResume: true, JobCancel: true}
	return &stubJobDriver{
		caps: caps,
		statusRes: &driver.StatusResult{
			State:        "idle",
			Errors:       []driver.StatusError{},
			Warnings:     []driver.StatusWarning{},
			Capabilities: caps,
		},
	}
}

func printingDriver() *stubJobDriver {
	drv := idleDriver()
	drv.statusRes.State = "printing"
	return drv
}

func pausedDriver() *stubJobDriver {
	drv := idleDriver()
	drv.statusRes.State = "paused"
	return drv
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

func makeDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver, prompter tty.Prompter) jobs.Deps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return jobs.Deps{
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

func runCmd(t *testing.T, deps jobs.Deps, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(jobs.CommandWithDeps(deps))
	buf := &strings.Builder{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"jobs"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- jobs pause ---

func TestJobsPause_WhenPrinting_Success(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "pause", "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Job paused") {
		t.Errorf("expected 'Job paused' in output, got:\n%s", out)
	}
}

func TestJobsPause_JSONSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "pause", "myprinter", "--yes", "--output", "json")
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
	if data["action"] != "pause" {
		t.Errorf("expected action=pause, got %v", data["action"])
	}
	if data["state"] != "paused" {
		t.Errorf("expected state=paused, got %v", data["state"])
	}
}

func TestJobsPause_WhenIdle_InvalidState(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, idleDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "pause", "myprinter", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error when not printing")
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

func TestJobsPause_ConfirmationDeclined_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: true, Lines: []string{"no"}})
	_, err := runCmd(t, deps, "pause", "myprinter")
	if err == nil {
		t.Fatal("expected error when confirmation declined")
	}
}

func TestJobsPause_NonInteractive_WithoutYes_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: false})
	_, err := runCmd(t, deps, "pause", "myprinter")
	if err == nil {
		t.Fatal("expected error in non-interactive mode")
	}
}

// --- jobs resume ---

func TestJobsResume_WhenPaused_Success(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, pausedDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "resume", "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Job resumed") {
		t.Errorf("expected 'Job resumed' in output, got:\n%s", out)
	}
}

func TestJobsResume_WhenPrinting_InvalidState(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "resume", "myprinter", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error when already printing")
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

// --- jobs cancel ---

func TestJobsCancel_WhenPrinting_Success(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "cancel", "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Job canceled") {
		t.Errorf("expected 'Job canceled' in output, got:\n%s", out)
	}
}

func TestJobsCancel_WhenPaused_Success(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, pausedDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "cancel", "myprinter", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Job canceled") {
		t.Errorf("expected 'Job canceled' in output, got:\n%s", out)
	}
}

func TestJobsCancel_WhenIdle_InvalidState(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, idleDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "cancel", "myprinter", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error when idle")
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

// --- jobs start ---

func TestJobsStart_WhenIdle_Success(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, idleDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "start", "myprinter", "sdcard:/models/cube.3mf", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "Job started") {
		t.Errorf("expected 'Job started' in output, got:\n%s", out)
	}
}

func TestJobsStart_JSONSuccess(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, idleDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "start", "myprinter", "sdcard:/models/cube.3mf", "--yes", "--output", "json")
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
	if data["action"] != "start" {
		t.Errorf("expected action=start, got %v", data["action"])
	}
	if data["devicePath"] != "sdcard:/models/cube.3mf" {
		t.Errorf("expected devicePath in data, got %v", data["devicePath"])
	}
}

func TestJobsStart_WhenPrinting_InvalidState(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, printingDriver(), &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "start", "myprinter", "sdcard:/models/cube.3mf", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error when not idle")
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

func TestJobsStart_MissingDevicePath_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, idleDriver(), &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "start", "myprinter", "--yes")
	if err == nil {
		t.Fatal("expected error for missing device path")
	}
}

func TestJobsStart_InvalidDevicePath_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	deps := makeDeps(t, dir, kc, idleDriver(), &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "start", "myprinter", "notapath", "--yes")
	if err == nil {
		t.Fatal("expected error for invalid device path")
	}
}

func TestJobsStart_ActionFailed_ReportsJobActionFailed(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := idleDriver()
	drv.startRes = &driver.JobActionResult{State: "error", Capabilities: drv.caps}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "start", "myprinter", "sdcard:/models/cube.3mf", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error when action fails")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "job_action_failed" {
		t.Errorf("expected job_action_failed, got %v", errObj["code"])
	}
}

func TestJobsCancel_ActionFailed_ReportsJobActionFailed(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := printingDriver()
	drv.cancelRes = &driver.JobActionResult{State: "error", Capabilities: drv.caps}
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	out, err := runCmd(t, deps, "cancel", "myprinter", "--yes", "--output", "json")
	if err == nil {
		t.Fatal("expected error when cancel action fails")
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errObj := env["error"].(map[string]any)
	if errObj["code"] != "job_action_failed" {
		t.Errorf("expected job_action_failed, got %v", errObj["code"])
	}
}

func TestJobsStart_UnsupportedCapability_Fails(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter")
	drv := idleDriver()
	drv.caps = driver.Capabilities{} // no JobStart
	drv.statusRes.Capabilities = drv.caps
	deps := makeDeps(t, dir, kc, drv, &tty.Mock{Terminal: true})
	_, err := runCmd(t, deps, "start", "myprinter", "sdcard:/models/cube.3mf", "--yes")
	if err == nil {
		t.Fatal("expected capability error")
	}
	e, ok := err.(*apperr.ExitError)
	if !ok || e.Code != 5 {
		t.Errorf("expected exit code 5, got %v", err)
	}
}

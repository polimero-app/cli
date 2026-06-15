package printer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

// stubStatusDriver satisfies driver.Driver for status command tests.
type stubStatusDriver struct {
	result *driver.StatusResult
	err    error
	caps   driver.Capabilities
}

func (s *stubStatusDriver) Name() string                      { return "bambu-lan" }
func (s *stubStatusDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubStatusDriver) ConnectCheck(_ context.Context, _, _, _ string, _ bool, _ time.Duration) (string, error) {
	return "", nil
}
func (s *stubStatusDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return s.result, s.err
}

func (s *stubStatusDriver) CaptureFingerprint(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func (s *stubStatusDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}

func defaultStatusDriver() *stubStatusDriver {
	nozzleTarget := 220.0
	bedTarget := 60.0
	layer := 10
	total := 50
	return &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State: "printing",
			Temperatures: &driver.Temperatures{
				Nozzle: &driver.Temperature{CurrentCelsius: 215.0, TargetCelsius: &nozzleTarget},
				Bed:    &driver.Temperature{CurrentCelsius: 60.0, TargetCelsius: &bedTarget},
			},
			Job:          &driver.Job{Name: "bracket.3mf"},
			Progress:     &driver.Progress{Percent: 42, CurrentLayer: &layer, TotalLayers: &total},
			Errors:       []driver.StatusError{},
			Warnings:     []driver.StatusWarning{},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
}

func statusDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver) printer.StatusDeps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return printer.StatusDeps{
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

func testRootForStatus(deps printer.StatusDeps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	sub := &cobra.Command{Use: "printer"}
	sub.AddCommand(printer.StatusCommandWithDeps(deps))
	root.AddCommand(sub)
	return root
}

func runStatusCmd(t *testing.T, deps printer.StatusDeps, args ...string) (string, error) {
	t.Helper()
	root := testRootForStatus(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "status"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Tests ---

func TestStatus_NoArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_TooManyArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "one", "two")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_InvalidProfileName_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "_invalid")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_ProfileNotFound_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "nonexistent")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_MissingAccessCode_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:access-code")
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_MissingTLSFingerprint_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false) // secure profile
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint")
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_InvalidTLSFingerprint_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false) // secure profile
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint", "")
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3 for invalid TLS fingerprint, got %v", err)
	}
}

func TestStatus_InsecureProfile_SkipsFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true) // insecure: no fingerprint stored
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("expected success for insecure profile, got: %v", err)
	}
}

func TestStatus_InsecureFlag_SkipsFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)                                            // secure profile in config
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint") // but fingerprint missing
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter", "--insecure")
	if err != nil {
		t.Fatalf("expected success with --insecure flag, got: %v", err)
	}
}

func TestStatus_CapabilityUnsupported_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{caps: driver.Capabilities{Status: false}}
	deps := statusDeps(t, dir, kc, drv)
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit 5, got %v", err)
	}
}

func TestStatus_AuthFailure_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(3, "MQTT authentication rejected"),
	}
	deps := statusDeps(t, dir, kc, drv)
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_NetworkTimeout_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "status check timed out"),
	}
	deps := statusDeps(t, dir, kc, drv)
	_, err := runStatusCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestStatus_HumanOutput_FullResult(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Printer: myprinter",
		"State: printing",
		"Progress: 42%",
		"Nozzle: 215.0 C / 220.0 C",
		"Bed: 60.0 C / 60.0 C",
		"Job: bracket.3mf",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestStatus_HumanOutput_WithErrors(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State:        "error",
			Errors:       []driver.StatusError{{Code: "hms:00000001:00000002", Message: "hardware error"}},
			Warnings:     []driver.StatusWarning{},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
	deps := statusDeps(t, dir, kc, drv)
	out, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"State: error", "Errors:", "- hms:00000001:00000002 hardware error"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestStatus_HumanOutput_WithWarnings(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State:        "idle",
			Errors:       []driver.StatusError{},
			Warnings:     []driver.StatusWarning{{Code: "low_filament", Message: "filament running low"}},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
	deps := statusDeps(t, dir, kc, drv)
	out, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("Warnings:")) {
		t.Errorf("expected 'Warnings:' header in output:\n%s", out)
	}
	if !bytes.Contains([]byte(out), []byte("- filament running low")) {
		t.Errorf("expected '- filament running low' in output:\n%s", out)
	}
}

func TestStatus_JSON_Envelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter", "--output", "json")
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
	if env["data"] == nil {
		t.Error("data must be present")
	}
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want map", env["data"])
	}
	if data["profile"] != "myprinter" {
		t.Errorf("data.profile = %v, want myprinter", data["profile"])
	}
	if data["driver"] != "bambu-lan" {
		t.Errorf("data.driver = %v, want bambu-lan", data["driver"])
	}
	meta, ok := env["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", env["meta"])
	}
	if meta["command"] != "printer status" {
		t.Errorf("meta.command = %v, want printer status", meta["command"])
	}
	if meta["durationMs"] == nil {
		t.Error("meta.durationMs must be present for successful status call")
	}
}

func TestStatus_JSON_ErrorEnvelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "status check timed out"),
	}
	deps := statusDeps(t, dir, kc, drv)
	out, err := runStatusCmd(t, deps, "myprinter", "--output", "json")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	errData, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error is %T, want map", env["error"])
	}
	if errData["code"] != "timeout" {
		t.Errorf("error.code = %v, want timeout", errData["code"])
	}
	if errData["message"] != "printer status request timed out" {
		t.Errorf("error.message = %v, want printer status request timed out", errData["message"])
	}
	details, ok := errData["details"].(map[string]any)
	if !ok {
		t.Fatalf("error.details is %T, want map", errData["details"])
	}
	if details["profile"] != "myprinter" {
		t.Errorf("details.profile = %v, want myprinter", details["profile"])
	}
	if details["timeout"] != "10s" {
		t.Errorf("details.timeout = %v, want 10s", details["timeout"])
	}
	meta, ok := env["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is %T, want map", env["meta"])
	}
	if meta["command"] != "printer status" {
		t.Errorf("meta.command = %v, want printer status", meta["command"])
	}
	if meta["durationMs"] != nil {
		t.Errorf("meta.durationMs should be absent in error envelope, got %v", meta["durationMs"])
	}
}

func TestStatus_JSON_NetworkErrorIsSanitized(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubStatusDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "connection failed: dial tcp 192.0.2.10:8883: secret-token"),
	}
	deps := statusDeps(t, dir, kc, drv)
	out, err := runStatusCmd(t, deps, "myprinter", "--output", "json")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	errData := env["error"].(map[string]any)
	if errData["message"] != "connection failed" {
		t.Fatalf("error.message = %v, want connection failed", errData["message"])
	}
	if strings.Contains(out, "secret-token") || strings.Contains(out, "192.0.2.10:8883") {
		t.Fatalf("raw transport detail leaked in output:\n%s", out)
	}
}

func TestStatus_TimeoutFlag_Override(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter", "--timeout", "30s")
	if err != nil {
		t.Fatalf("expected success with valid --timeout, got: %v", err)
	}
}

func TestStatus_TimeoutFlag_InvalidFormat_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter", "--timeout", "notaduration")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid --timeout, got %v", err)
	}
}

func TestStatus_TimeoutFlag_Zero_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	_, err := runStatusCmd(t, deps, "myprinter", "--timeout", "0s")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for zero --timeout, got %v", err)
	}
}

func TestStatus_Verbose_ShowsProgressSteps(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter", "--verbose")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Connecting to 192.0.2.10:8883...") {
		t.Errorf("expected 'Connecting to 192.0.2.10:8883...' in output:\n%s", out)
	}
	if !strings.Contains(out, "Response received (") {
		t.Errorf("expected 'Response received (' in output:\n%s", out)
	}
}

func TestStatus_Verbose_SuppressedInJSONMode(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter", "--verbose", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Connecting") {
		t.Errorf("expected no 'Connecting' in JSON mode output:\n%s", out)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
}

func TestStatus_NoVerbose_NoProgressLines(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := statusDeps(t, dir, kc, defaultStatusDriver())
	out, err := runStatusCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Connecting") {
		t.Errorf("expected no 'Connecting' in non-verbose output:\n%s", out)
	}
}

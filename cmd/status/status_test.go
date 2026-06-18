package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/status"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/spf13/cobra"
)

const testFingerprint = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// stubDriver satisfies driver.Driver for status command tests.
type stubDriver struct {
	result *driver.StatusResult
	err    error
	caps   driver.Capabilities
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
	return s.result, s.err
}

func (s *stubDriver) CaptureFingerprint(_ context.Context, _ driver.ProfileInput) (string, error) {
	return "", nil
}

func (s *stubDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return []driver.DiscoveredPrinter{}, nil
}

func (s *stubDriver) CameraStream(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.CameraStreamResult, error) {
	return nil, nil
}

func defaultDriver() *stubDriver {
	nozzleTarget := 220.0
	bedTarget := 60.0
	layer := 10
	total := 50
	return &stubDriver{
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

func makeDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver) status.Deps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return status.Deps{
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

func testRoot(deps status.Deps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(status.CommandWithDeps(deps))
	return root
}

func runCmd(t *testing.T, deps status.Deps, args ...string) (string, error) {
	t.Helper()
	root := testRoot(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"status"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Tests ---

func TestStatus_NoArgs_ShowsHelp(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps)
	if err != nil {
		t.Errorf("expected no error (help), got %v", err)
	}
	if !strings.Contains(out, "status <name>") {
		t.Errorf("expected usage line in help output:\n%s", out)
	}
}

func TestStatus_TooManyArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "one", "two")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_InvalidOutputFormat_PrintsError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "myprinter", "--output", "xml")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(out, "must be human or json") {
		t.Errorf("expected error message naming valid --output values, got:\n%s", out)
	}
}

func TestStatus_InvalidOutputFormat_TooManyArgs_PrintsError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "one", "two", "--output", "xml")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(out, "must be human or json") {
		t.Errorf("expected error message naming valid --output values, got:\n%s", out)
	}
}

func TestStatus_InvalidProfileName_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "_invalid")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestStatus_ProfileNotFound_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "nonexistent")
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
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_MissingTLSFingerprint_ExitsCode3(t *testing.T) {
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

func TestStatus_InvalidTLSFingerprint_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint", "")
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3 for invalid TLS fingerprint, got %v", err)
	}
}

func TestStatus_InsecureProfile_SkipsFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("expected success for insecure profile, got: %v", err)
	}
}

func TestStatus_InsecureFlag_SkipsFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:tls-fingerprint")
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--insecure")
	if err != nil {
		t.Fatalf("expected success with --insecure flag, got: %v", err)
	}
}

func TestStatus_CapabilityUnsupported_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubDriver{caps: driver.Capabilities{Status: false}}
	deps := makeDeps(t, dir, kc, drv)
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 5 {
		t.Errorf("expected exit 5, got %v", err)
	}
}

func TestStatus_AuthFailure_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(3, "MQTT authentication rejected"),
	}
	deps := makeDeps(t, dir, kc, drv)
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestStatus_NetworkTimeout_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "status check timed out"),
	}
	deps := makeDeps(t, dir, kc, drv)
	_, err := runCmd(t, deps, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
}

func TestStatus_HumanOutput_FullResult(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "myprinter")
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
	drv := &stubDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State:        "error",
			Errors:       []driver.StatusError{{Code: "hms:00000001:00000002", Message: "hardware error"}},
			Warnings:     []driver.StatusWarning{},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)
	out, err := runCmd(t, deps, "myprinter")
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
	drv := &stubDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State:        "idle",
			Errors:       []driver.StatusError{},
			Warnings:     []driver.StatusWarning{{Code: "low_filament", Message: "filament running low"}},
			Capabilities: driver.Capabilities{Status: true},
		},
	}
	deps := makeDeps(t, dir, kc, drv)
	out, err := runCmd(t, deps, "myprinter")
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
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "myprinter", "--output", "json")
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
	if meta["command"] != "status" {
		t.Errorf("meta.command = %v, want status", meta["command"])
	}
	if meta["durationMs"] == nil {
		t.Error("meta.durationMs must be present for successful status call")
	}
}

func TestStatus_JSON_ErrorEnvelope(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "status check timed out"),
	}
	deps := makeDeps(t, dir, kc, drv)
	out, err := runCmd(t, deps, "myprinter", "--output", "json")
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
	if errData["message"] != "status request timed out" {
		t.Errorf("error.message = %v, want 'status request timed out'", errData["message"])
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
	if meta["command"] != "status" {
		t.Errorf("meta.command = %v, want status", meta["command"])
	}
	if meta["durationMs"] != nil {
		t.Errorf("meta.durationMs should be absent in error envelope, got %v", meta["durationMs"])
	}
}

func TestStatus_JSON_NetworkErrorIsSanitized(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	drv := &stubDriver{
		caps: driver.Capabilities{Status: true},
		err:  apperr.New(4, "connection failed: dial tcp 192.0.2.10:8883: secret-token"),
	}
	deps := makeDeps(t, dir, kc, drv)
	out, err := runCmd(t, deps, "myprinter", "--output", "json")
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
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--timeout", "30s")
	if err != nil {
		t.Fatalf("expected success with valid --timeout, got: %v", err)
	}
}

func TestStatus_TimeoutFlag_InvalidFormat_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--timeout", "notaduration")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid --timeout, got %v", err)
	}
}

func TestStatus_TimeoutFlag_Zero_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, defaultDriver())
	_, err := runCmd(t, deps, "myprinter", "--timeout", "0s")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for zero --timeout, got %v", err)
	}
}

func TestStatus_Verbose_ShowsProgressSteps(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "myprinter", "--verbose")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Connecting to 192.0.2.10...") {
		t.Errorf("expected 'Connecting to 192.0.2.10...' in output:\n%s", out)
	}
	if !strings.Contains(out, "Response received (") {
		t.Errorf("expected 'Response received (' in output:\n%s", out)
	}
}

func TestStatus_Verbose_SuppressedInJSONMode(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "myprinter", "--verbose", "--output", "json")
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
	deps := makeDeps(t, dir, kc, defaultDriver())
	out, err := runCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "Connecting") {
		t.Errorf("expected no 'Connecting' in non-verbose output:\n%s", out)
	}
}

func detailedDriver() *stubDriver {
	nozzleTarget := 220.0
	bedTarget := 60.0
	layer := 84
	total := 200
	remaining := 6000
	totalTime := 10320
	speed := "standard"
	stage := "printing"
	nozzleDiam := 0.4
	bedType := "textured_pei"
	fileSize := 14893261
	progress := 35
	ready := false
	humidityRange := "20-30%"
	humidityLevel := "moderate"
	temp := 28.0
	filType := "PLA"
	color := "FF0000"
	remain := 85
	nMin := 190
	nMax := 230

	return &stubDriver{
		caps: driver.Capabilities{Status: true},
		result: &driver.StatusResult{
			State: "printing",
			Temperatures: &driver.Temperatures{
				Nozzle:  &driver.Temperature{CurrentCelsius: 215.0, TargetCelsius: &nozzleTarget},
				Bed:     &driver.Temperature{CurrentCelsius: 60.0, TargetCelsius: &bedTarget},
				Chamber: &driver.Temperature{CurrentCelsius: 38.0},
			},
			Job:      &driver.Job{Name: "bracket.3mf"},
			Progress: &driver.Progress{Percent: 42, CurrentLayer: &layer, TotalLayers: &total},
			Errors:   []driver.StatusError{},
			Warnings: []driver.StatusWarning{},
			Capabilities: driver.Capabilities{Status: true},
			Fans: driver.Fans{
				"partCooling": 100,
				"heatbreak":   70,
				"auxiliary":    50,
				"chamber":     30,
			},
			TimeEstimates: &driver.TimeEstimates{
				ElapsedSeconds:   4320,
				RemainingSeconds: &remaining,
				TotalSeconds:     &totalTime,
			},
			SpeedLevel: &speed,
			Wifi:       &driver.Wifi{SignalDbm: -45},
			Lights:     driver.Lights{"chamber_light": "on"},
			PrintMeta: &driver.PrintMeta{
				FileName:       "bracket.3mf",
				FileSize:       &fileSize,
				NozzleDiameter: &nozzleDiam,
				BedType:        &bedType,
			},
			Stage: &stage,
			Timelapse: &driver.Timelapse{
				Recording: true,
				Progress:  &progress,
				Ready:     &ready,
			},
			GcodePosition: &driver.GcodePosition{
				ZMm:         12.4,
				CurrentLine: 48201,
				TotalLines:  112400,
			},
			Extensions: map[string]any{
				"bambu-lan": &driver.BambuExtension{
					AMS: &driver.AMSData{
						Units: []driver.AMSUnit{
							{
								ID:          0,
								HumidityRange: &humidityRange,
								HumidityLevel: &humidityLevel,
								Temperature: &temp,
								Trays: []driver.AMSTray{
									{Slot: 0, FilamentType: &filType, Color: &color, RemainingPercent: &remain, NozzleTempMin: &nMin, NozzleTempMax: &nMax},
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestStatus_Detailed_HumanOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, detailedDriver())
	out, err := runCmd(t, deps, "myprinter", "--detailed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Stage: printing",
		"Progress: 42% (layer 84 / 200)",
		"Speed: standard",
		"Time:",
		"elapsed",
		"remaining",
		"Fans:",
		"Part cooling: 100%",
		"Heatbreak: 70%",
		"Wi-Fi: -45 dBm",
		"Lights:",
		"chamber_light: on",
		"Job: bracket.3mf (14.2 MB, 0.4mm nozzle, textured_pei)",
		"G-code: Z 12.40 mm, line 48201 / 112400",
		"Timelapse: recording (35%)",
		"AMS:",
		"Unit 0",
		"humidity: 20-30% [moderate]",
		"Slot 0: PLA FF0000 (85%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestStatus_Detailed_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, detailedDriver())
	out, err := runCmd(t, deps, "myprinter", "--detailed", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	data := env["data"].(map[string]any)
	if data["speedLevel"] != "standard" {
		t.Errorf("speedLevel = %v, want standard", data["speedLevel"])
	}
	if data["stage"] != "printing" {
		t.Errorf("stage = %v, want printing", data["stage"])
	}
	if data["fans"] == nil {
		t.Error("expected fans in detailed JSON output")
	}
	if data["timeEstimates"] == nil {
		t.Error("expected timeEstimates in detailed JSON output")
	}
	if data["wifi"] == nil {
		t.Error("expected wifi in detailed JSON output")
	}
	if data["lights"] == nil {
		t.Error("expected lights in detailed JSON output")
	}
	if data["printMeta"] == nil {
		t.Error("expected printMeta in detailed JSON output")
	}
	if data["timelapse"] == nil {
		t.Error("expected timelapse in detailed JSON output")
	}
	if data["gcodePosition"] == nil {
		t.Error("expected gcodePosition in detailed JSON output")
	}
	if data["extensions"] == nil {
		t.Error("expected extensions in detailed JSON output")
	}
}

func TestStatus_NoDetailed_OmitsExtendedFields(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, detailedDriver())
	out, err := runCmd(t, deps, "myprinter", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	data := env["data"].(map[string]any)
	for _, field := range []string{"fans", "timeEstimates", "speedLevel", "wifi", "lights", "printMeta", "stage", "timelapse", "gcodePosition", "extensions"} {
		if data[field] != nil {
			t.Errorf("expected %q to be absent without --detailed, got %v", field, data[field])
		}
	}
}

func TestStatus_NoDetailed_HumanOmitsExtendedSections(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true)
	deps := makeDeps(t, dir, kc, detailedDriver())
	out, err := runCmd(t, deps, "myprinter")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, absent := range []string{"Fans:", "Speed:", "Time:", "Wi-Fi:", "Lights:", "G-code:", "Timelapse:", "AMS:", "Stage:"} {
		if strings.Contains(out, absent) {
			t.Errorf("expected %q to be absent without --detailed, got output:\n%s", absent, out)
		}
	}
	// But summary fields should still be present.
	for _, present := range []string{"Printer: myprinter", "State: printing", "Progress: 42%"} {
		if !strings.Contains(out, present) {
			t.Errorf("expected %q in output:\n%s", present, out)
		}
	}
}

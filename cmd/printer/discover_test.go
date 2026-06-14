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
	"github.com/spf13/cobra"
)

// stubDiscoverDriver satisfies driver.Driver for discover command tests.
type stubDiscoverDriver struct {
	name    string
	caps    driver.Capabilities
	found   []driver.DiscoveredPrinter
	discErr error
}

func (s *stubDiscoverDriver) Name() string                      { return s.name }
func (s *stubDiscoverDriver) Capabilities() driver.Capabilities { return s.caps }
func (s *stubDiscoverDriver) ConnectCheck(_ context.Context, _, _, _ string, _ bool, _ time.Duration) (string, error) {
	return "", nil
}
func (s *stubDiscoverDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, nil
}
func (s *stubDiscoverDriver) CaptureFingerprint(_ context.Context, _, _ string) (string, error) {
	return "", nil
}
func (s *stubDiscoverDriver) Discover(_ context.Context) ([]driver.DiscoveredPrinter, error) {
	return s.found, s.discErr
}

func defaultDiscoverDriver() *stubDiscoverDriver {
	return &stubDiscoverDriver{
		name: "bambu-lan",
		caps: driver.Capabilities{Discovery: true},
		found: []driver.DiscoveredPrinter{
			{Host: "192.0.2.10", Port: 8883, Serial: "01S09C450100XXX", Model: "X1C", Name: "My X1C", Driver: "bambu-lan"},
		},
	}
}

func discoverDeps(t *testing.T, dir string, drvs ...driver.Driver) printer.DiscoverDeps {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	return printer.DiscoverDeps{
		AllDrivers: func() []driver.Driver { return drvs },
		GetDriver: func(name string) (driver.Driver, bool) {
			for _, d := range drvs {
				if d.Name() == name {
					return d, true
				}
			}
			return nil, false
		},
	}
}

func testRootForDiscover(deps printer.DiscoverDeps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	sub := &cobra.Command{Use: "printer"}
	sub.AddCommand(printer.DiscoverCommandWithDeps(deps))
	root.AddCommand(sub)
	return root
}

func runDiscoverCmd(t *testing.T, deps printer.DiscoverDeps, args ...string) (string, error) {
	t.Helper()
	root := testRootForDiscover(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "discover"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func checkExitCode(t *testing.T, err error, want int) {
	t.Helper()
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != want {
		t.Errorf("exit code = %d, want %d", exitErr.Code, want)
	}
}

// --- Tests ---

func TestDiscover_ReturnsList(t *testing.T) {
	dir := t.TempDir()
	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	out, err := runDiscoverCmd(t, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains([]byte(out), []byte("01S09C450100XXX")) {
		t.Errorf("expected serial in output, got: %s", out)
	}
	if !bytes.Contains([]byte(out), []byte("192.0.2.10")) {
		t.Errorf("expected host in output, got: %s", out)
	}
}

func TestDiscover_EmptyResult_ExitsZero(t *testing.T) {
	dir := t.TempDir()
	drv := &stubDiscoverDriver{name: "bambu-lan", caps: driver.Capabilities{Discovery: true}, found: []driver.DiscoveredPrinter{}}
	deps := discoverDeps(t, dir, drv)
	_, err := runDiscoverCmd(t, deps)
	if err != nil {
		t.Fatalf("expected exit 0 on empty result, got: %v", err)
	}
}

func TestDiscover_SetsConfiguredAs_WhenSerialMatches(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)

	c, _ := config.Open(dir)
	_ = c.AddProfile("garage-x1c", config.Profile{
		Driver:  "bambu-lan",
		Host:    "192.0.2.10",
		Serial:  "01S09C450100XXX",
		Timeout: "10s",
	})
	_ = config.Save(dir, c)

	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	out, err := runDiscoverCmd(t, deps, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env struct {
		Data struct {
			Printers []struct {
				ConfiguredAs *string `json:"configuredAs"`
			} `json:"printers"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", jsonErr, out)
	}
	if len(env.Data.Printers) == 0 {
		t.Fatal("expected at least one printer in JSON output")
	}
	p := env.Data.Printers[0]
	if p.ConfiguredAs == nil || *p.ConfiguredAs != "garage-x1c" {
		t.Errorf("configuredAs = %v, want %q", p.ConfiguredAs, "garage-x1c")
	}
}

func TestDiscover_ConfiguredAs_NullWhenNoMatch(t *testing.T) {
	dir := t.TempDir()
	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	out, err := runDiscoverCmd(t, deps, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env struct {
		Data struct {
			Printers []struct {
				ConfiguredAs *string `json:"configuredAs"`
			} `json:"printers"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", jsonErr, out)
	}
	if len(env.Data.Printers) == 0 {
		t.Fatal("expected at least one printer")
	}
	if env.Data.Printers[0].ConfiguredAs != nil {
		t.Errorf("configuredAs = %v, want null", *env.Data.Printers[0].ConfiguredAs)
	}
}

func TestDiscover_UnknownDriver_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	_, err := runDiscoverCmd(t, deps, "--driver", "no-such-driver")
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	checkExitCode(t, err, 2)
}

func TestDiscover_DriverNoDiscovery_ExitsCode5(t *testing.T) {
	dir := t.TempDir()
	noDiscDrv := &stubDiscoverDriver{name: "bambu-lan", caps: driver.Capabilities{Discovery: false}}
	deps := discoverDeps(t, dir, noDiscDrv)
	_, err := runDiscoverCmd(t, deps, "--driver", "bambu-lan")
	if err == nil {
		t.Fatal("expected error for driver without discovery")
	}
	checkExitCode(t, err, 5)
}

func TestDiscover_InvalidTimeout_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	_, err := runDiscoverCmd(t, deps, "--timeout", "not-a-duration")
	if err == nil {
		t.Fatal("expected error for invalid timeout")
	}
	checkExitCode(t, err, 2)
}

func TestDiscover_ZeroTimeout_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	_, err := runDiscoverCmd(t, deps, "--timeout", "0s")
	if err == nil {
		t.Fatal("expected error for zero timeout")
	}
	checkExitCode(t, err, 2)
}

func TestDiscover_DriverError_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	drv := &stubDiscoverDriver{
		name: "bambu-lan",
		caps: driver.Capabilities{Discovery: true},
		discErr: apperr.New(4, "mDNS socket unavailable"),
	}
	deps := discoverDeps(t, dir, drv)
	out, err := runDiscoverCmd(t, deps, "--output", "json")
	if err == nil {
		t.Fatal("expected error when driver.Discover fails")
	}
	checkExitCode(t, err, 4)

	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", jsonErr, out)
	}
	if env.OK {
		t.Error("expected ok=false in error envelope")
	}
	if env.Error == nil || env.Error.Code != "connection_failed" {
		t.Errorf("error code = %v, want %q", env.Error, "connection_failed")
	}
}

func TestDiscover_JSON_EmptyPrintersArray(t *testing.T) {
	dir := t.TempDir()
	drv := &stubDiscoverDriver{name: "bambu-lan", caps: driver.Capabilities{Discovery: true}, found: []driver.DiscoveredPrinter{}}
	deps := discoverDeps(t, dir, drv)
	out, err := runDiscoverCmd(t, deps, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Printers []any `json:"printers"`
			Count    int   `json:"count"`
		} `json:"data"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", jsonErr, out)
	}
	if !env.OK {
		t.Error("expected ok=true")
	}
	if env.Data.Printers == nil {
		t.Error("expected non-null printers array")
	}
	if len(env.Data.Printers) != 0 {
		t.Errorf("expected 0 printers, got %d", len(env.Data.Printers))
	}
}

func TestDiscover_JSON_IncludesDurationMs(t *testing.T) {
	dir := t.TempDir()
	deps := discoverDeps(t, dir, defaultDiscoverDriver())
	out, err := runDiscoverCmd(t, deps, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env struct {
		Meta struct {
			DurationMs *int64 `json:"durationMs"`
		} `json:"meta"`
	}
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v", jsonErr)
	}
	if env.Meta.DurationMs == nil {
		t.Error("expected durationMs in meta")
	}
}

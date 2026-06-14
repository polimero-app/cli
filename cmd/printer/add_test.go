package printer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// stubDriver satisfies driver.Driver for tests.
type stubDriver struct {
	fingerprint string
	err         error
}

func (s *stubDriver) Name() string { return "bambu-lan" }
func (s *stubDriver) ConnectCheck(_ context.Context, _, _, _ string, insecure bool, _ time.Duration) (string, error) {
	if insecure {
		return "", nil
	}
	return s.fingerprint, s.err
}
func (s *stubDriver) Capabilities() driver.Capabilities { return driver.Capabilities{} }
func (s *stubDriver) Status(_ context.Context, _ driver.ProfileInput, _ driver.SecretsBundle, _ *slog.Logger) (*driver.StatusResult, error) {
	return nil, nil
}

func (s *stubDriver) CaptureFingerprint(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

func defaultAddDeps(kc *keychain.Mock, p *tty.Mock) printer.AddDeps {
	return printer.AddDeps{
		KC:       kc,
		Prompter: p,
		GetDriver: func(name string) (driver.Driver, bool) {
			if name == "bambu-lan" {
				return &stubDriver{fingerprint: "sha256:aabbcc"}, true
			}
			return nil, false
		},
	}
}

func testRootForAdd(t *testing.T, dir string, deps printer.AddDeps) *cobra.Command {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	sub := &cobra.Command{Use: "printer"}
	sub.AddCommand(printer.AddCommandWithDeps(deps))
	root.AddCommand(sub)
	return root
}

func runAddCmd(t *testing.T, dir string, deps printer.AddDeps, args ...string) (string, error) {
	t.Helper()
	root := testRootForAdd(t, dir, deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "add"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// --- Golden path ---

func TestAdd_Human_SecurePath(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "testcode123"}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"garage-x1c", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Printer profile added: garage-x1c") {
		t.Errorf("expected success message, got:\n%s", out)
	}
	if !strings.Contains(out, "sha256:aabbcc") {
		t.Errorf("expected fingerprint in output, got:\n%s", out)
	}

	// Access code stored in keychain
	ac, err := kc.Get("polimero", "bambu-lan:garage-x1c:access-code")
	if err != nil {
		t.Fatalf("access code not in keychain: %v", err)
	}
	if ac != "testcode123" {
		t.Errorf("access code = %q, want testcode123", ac)
	}

	// Fingerprint stored in keychain
	fp, _ := kc.Get("polimero", "bambu-lan:garage-x1c:tls-fingerprint")
	if fp != "sha256:aabbcc" {
		t.Errorf("fingerprint = %q, want sha256:aabbcc", fp)
	}

	// Profile written to config
	cfg, err := os.ReadFile(filepath.Join(dir, "polimero.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "garage-x1c") {
		t.Errorf("profile not in config file:\n%s", cfg)
	}
}

func TestAdd_Human_InsecurePath(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "testcode123"}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001", "--insecure")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Warning: TLS verification is disabled") {
		t.Errorf("expected insecure warning, got:\n%s", out)
	}

	// TLS fingerprint must NOT be stored
	if _, err := kc.Get("polimero", "bambu-lan:myprinter:tls-fingerprint"); err == nil {
		t.Error("TLS fingerprint should not be stored for insecure profile")
	}
}

func TestAdd_AccessCodeFile(t *testing.T) {
	dir := t.TempDir()
	codeFile := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(codeFile, []byte("filecode\n"), 0600); err != nil {
		t.Fatal(err)
	}
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false} // non-interactive, but file provided
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--access-code-file", codeFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ac, _ := kc.Get("polimero", "bambu-lan:myprinter:access-code")
	if ac != "filecode" { // trailing \n stripped
		t.Errorf("access code = %q, want filecode", ac)
	}
}

func TestAdd_JSON_Success(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	data := env["data"].(map[string]any)
	prof := data["profile"].(map[string]any)
	if prof["name"] != "myprinter" {
		t.Errorf("profile.name = %v, want myprinter", prof["name"])
	}
	if prof["tlsFingerprint"] != "sha256:aabbcc" {
		t.Errorf("profile.tlsFingerprint = %v, want sha256:aabbcc", prof["tlsFingerprint"])
	}
}

func TestAdd_JSON_Insecure_NullFingerprint(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--insecure", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	json.Unmarshal([]byte(out), &env) //nolint:errcheck
	prof := env["data"].(map[string]any)["profile"].(map[string]any)
	fp, exists := prof["tlsFingerprint"]
	if !exists {
		t.Error("tlsFingerprint key missing from JSON")
	}
	if fp != nil {
		t.Errorf("tlsFingerprint = %v, want null for insecure", fp)
	}
}

// --- Validation errors ---

func TestAdd_DuplicateName(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	deps := defaultAddDeps(kc, p)
	_, _ = runAddCmd(t, dir, deps, "dup", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	_, err := runAddCmd(t, dir, deps, "dup", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestAdd_InvalidProfileName(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	// "_invalid" starts with '_', which fails the profile name regex.
	// Do NOT use a name starting with '-' — Cobra would misparse it as a flag.
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"_invalid", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid name, got %v", err)
	}
}

func TestAdd_TooManyArgsShowsHumanError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p), "one", "two")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for too many args, got %v", err)
	}
	if out == "" {
		t.Fatal("expected human error output")
	}
	if want := "Error: expected exactly one profile name, got 2"; !strings.Contains(out, want) {
		t.Fatalf("expected %q in output, got:\n%s", want, out)
	}
}

func TestAdd_TooManyArgsShowsJSONError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p), "one", "two", "--output", "json")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for too many args, got %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != false {
		t.Fatalf("ok = %v, want false", env["ok"])
	}
	errDetail := env["error"].(map[string]any)
	if errDetail["message"] != "expected exactly one profile name, got 2" {
		t.Fatalf("error.message = %v", errDetail["message"])
	}
}

func TestAdd_MissingDriver(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--host", "192.0.2.10", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for missing driver, got %v", err)
	}
}

func TestAdd_UnknownDriver(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "unknown", "--host", "192.0.2.10", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for unknown driver, got %v", err)
	}
}

func TestAdd_MissingHost(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for missing host, got %v", err)
	}
}

func TestAdd_InvalidHost(t *testing.T) {
	cases := []string{
		"bad host",
		"-printer.local",
		"printer..local",
		"999.999.999.999",
	}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			dir := t.TempDir()
			kc := keychain.NewMock()
			p := &tty.Mock{Terminal: true, HiddenVal: "code"}
			_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
				"myprinter", "--driver", "bambu-lan", "--host", host, "--serial", "SN001")
			var exitErr *apperr.ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 2 {
				t.Errorf("expected exit 2 for invalid host, got %v", err)
			}
		})
	}
}

func TestAdd_MissingSerial(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for missing serial, got %v", err)
	}
}

func TestAdd_InvalidSerialFormat(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN 001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid serial, got %v", err)
	}
}

func TestAdd_SerialTooLong(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", strings.Repeat("A", 65))
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for long serial, got %v", err)
	}
}

func TestAdd_InvalidTimeout(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001", "--timeout", "not-a-duration")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid timeout, got %v", err)
	}
}

func TestAdd_NonPositiveTimeout(t *testing.T) {
	for _, timeout := range []string{"0s", "-1s"} {
		t.Run(timeout, func(t *testing.T) {
			dir := t.TempDir()
			kc := keychain.NewMock()
			p := &tty.Mock{Terminal: true, HiddenVal: "code"}
			_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
				"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001", "--timeout", timeout)
			var exitErr *apperr.ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != 2 {
				t.Errorf("expected exit 2 for timeout %s, got %v", timeout, err)
			}
		})
	}
}

func TestAdd_NonInteractive_NoFile(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false} // not a terminal, no file
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for non-interactive without file, got %v", err)
	}
}

func TestAdd_ConnectivityFailure_ExitsCode4(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	deps := printer.AddDeps{
		KC:       kc,
		Prompter: p,
		GetDriver: func(name string) (driver.Driver, bool) {
			return &stubDriver{err: apperr.New(4, "connection failed")}, true
		},
	}
	_, err := runAddCmd(t, dir, deps,
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		t.Errorf("expected exit 4, got %v", err)
	}
	// Keychain must be untouched
	if _, err := kc.Get("polimero", "bambu-lan:myprinter:access-code"); err == nil {
		t.Error("keychain should not have been written on connectivity failure")
	}
}

func TestAdd_AuthFailure_ExitsCode3(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "badcode"}
	deps := printer.AddDeps{
		KC:       kc,
		Prompter: p,
		GetDriver: func(name string) (driver.Driver, bool) {
			return &stubDriver{err: apperr.New(3, "MQTT authentication rejected")}, true
		},
	}
	_, err := runAddCmd(t, dir, deps,
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Errorf("expected exit 3, got %v", err)
	}
}

func TestAdd_AccessCodeFile_InsecurePermissions(t *testing.T) {
	dir := t.TempDir()
	codeFile := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(codeFile, []byte("secret"), 0644); err != nil { // group-readable
		t.Fatal(err)
	}
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--access-code-file", codeFile)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for insecure file permissions, got %v", err)
	}
}

func TestAdd_AccessCodeFile_TooLarge(t *testing.T) {
	dir := t.TempDir()
	codeFile := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(codeFile, []byte(strings.Repeat("x", 4097)), 0600); err != nil {
		t.Fatal(err)
	}
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--access-code-file", codeFile, "--insecure")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for too-large access-code file, got %v", err)
	}
}

func TestAdd_AccessCodeFile_NotRegular(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	_, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001",
		"--access-code-file", dir, "--insecure")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for non-regular access-code file, got %v", err)
	}
}

func TestAdd_RollbackOnFingerprintFail(t *testing.T) {
	dir := t.TempDir()
	// Mock keychain that fails on the second Set (fingerprint write)
	kc := &failOnSecondSetMock{inner: keychain.NewMock()}
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	deps := printer.AddDeps{
		KC:       kc,
		Prompter: p,
		GetDriver: func(name string) (driver.Driver, bool) {
			return &stubDriver{fingerprint: "sha256:aabbcc"}, true
		},
	}
	_, err := runAddCmd(t, dir, deps,
		"myprinter", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	if err == nil {
		t.Fatal("expected error from keychain failure")
	}
	// access code must be rolled back
	if _, err := kc.inner.Get("polimero", "bambu-lan:myprinter:access-code"); err == nil {
		t.Error("access code should have been rolled back")
	}
}

// failOnSecondSetMock fails on the second Set call (simulates fingerprint write failure).
type failOnSecondSetMock struct {
	inner    *keychain.Mock
	setCalls int
}

func (m *failOnSecondSetMock) Get(svc, acct string) (string, error) { return m.inner.Get(svc, acct) }
func (m *failOnSecondSetMock) Delete(svc, acct string) error        { return m.inner.Delete(svc, acct) }
func (m *failOnSecondSetMock) Set(svc, acct, secret string) error {
	m.setCalls++
	if m.setCalls >= 2 {
		return fmt.Errorf("keychain unavailable")
	}
	return m.inner.Set(svc, acct, secret)
}

func TestAdd_NameNormalisedToLowercase(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, HiddenVal: "code"}
	out, err := runAddCmd(t, dir, defaultAddDeps(kc, p),
		"UPPER", "--driver", "bambu-lan", "--host", "192.0.2.10", "--serial", "SN001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "upper") {
		t.Errorf("expected lowercase name in output, got:\n%s", out)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "polimero.yaml"))
	if !strings.Contains(string(cfg), "upper:") {
		t.Errorf("expected lowercase profile key in config:\n%s", cfg)
	}
}

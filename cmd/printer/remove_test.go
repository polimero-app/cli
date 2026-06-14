package printer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

func testRootForRemove(t *testing.T, dir string, deps printer.RemoveDeps) *cobra.Command {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	sub := &cobra.Command{Use: "printer"}
	sub.AddCommand(printer.RemoveCommandWithDeps(deps))
	root.AddCommand(sub)
	return root
}

func runRemoveCmd(t *testing.T, dir string, deps printer.RemoveDeps, args ...string) (string, error) {
	t.Helper()
	root := testRootForRemove(t, dir, deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "remove"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// seedProfile writes a profile and its keychain entries for a test scenario.
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
	_ = kc.Set("polimero", "bambu-lan:"+name+":access-code", "testcode")
	if !insecure {
		_ = kc.Set("polimero", "bambu-lan:"+name+":tls-fingerprint", "sha256:aabbcc")
	}
}

func TestRemove_HappyPath_SecureProfile(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "garage-x1c", false)
	p := &tty.Mock{Terminal: true, Lines: []string{"yes"}}

	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "garage-x1c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected success output")
	}

	// Profile must be gone
	cfg, _ := config.Open(dir)
	if _, ok := cfg.GetProfile("garage-x1c"); ok {
		t.Error("profile still present after remove")
	}
	// Keychain entries must be gone
	if _, err := kc.Get("polimero", "bambu-lan:garage-x1c:access-code"); err == nil {
		t.Error("access code still in keychain")
	}
	if _, err := kc.Get("polimero", "bambu-lan:garage-x1c:tls-fingerprint"); err == nil {
		t.Error("TLS fingerprint still in keychain")
	}
}

func TestRemove_YesFlag_SkipsConfirmation(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	p := &tty.Mock{Terminal: false} // non-interactive

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "myprinter", "--yes")
	if err != nil {
		t.Fatalf("expected success with --yes, got: %v", err)
	}
}

func TestRemove_NonInteractive_NoYes_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	p := &tty.Mock{Terminal: false}

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2, got %v", err)
	}
}

func TestRemove_ConfirmationDeclined(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	p := &tty.Mock{Terminal: true, Lines: []string{"no"}}

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "myprinter")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 when confirmation declined, got %v", err)
	}
	// Profile must still exist
	cfg, _ := config.Open(dir)
	if _, ok := cfg.GetProfile("myprinter"); !ok {
		t.Error("profile should not have been removed when confirmation declined")
	}
}

func TestRemove_ProfileNotFound(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, Lines: []string{"yes"}}
	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "nonexistent", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for missing profile, got %v", err)
	}
}

func TestRemove_InvalidProfileName(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, Lines: []string{"yes"}}
	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "_invalid", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid profile name, got %v", err)
	}
}

func TestRemove_InvalidProfileNameSkipsPromptAndKeychain(t *testing.T) {
	dir := t.TempDir()
	kc := &trackingKeychain{inner: keychain.NewMock()}
	seedProfile(t, dir, kc.inner, "_invalid", false)
	p := &trackingPrompter{terminal: true, lines: []string{"yes"}}

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "_invalid")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for invalid profile name, got %v", err)
	}
	if p.readLineCalls != 0 {
		t.Fatalf("prompt should not be used for invalid profile name")
	}
	if kc.deleteCalls != 0 {
		t.Fatalf("keychain should not be touched for invalid profile name")
	}
	cfg, cfgErr := config.Open(dir)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if _, ok := cfg.GetProfile("_invalid"); !ok {
		t.Fatal("invalid profile fixture should remain in config")
	}
}

func TestRemove_MissingAccessCode_Warning(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete("polimero", "bambu-lan:myprinter:access-code") // simulate missing entry

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes")
	if err != nil {
		t.Fatalf("expected success despite missing access code, got: %v", err)
	}
}

func TestRemove_JSON_Warnings(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)
	_ = kc.Delete("polimero", "bambu-lan:myprinter:access-code")

	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes", "--output", "json")
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
	data := env["data"].(map[string]any)

	removed := data["removed"].(map[string]any)
	if removed["accessCodeRemoved"] != false {
		t.Errorf("accessCodeRemoved = %v, want false", removed["accessCodeRemoved"])
	}
	if removed["tlsFingerprintRemoved"] != true {
		t.Errorf("tlsFingerprintRemoved = %v, want true", removed["tlsFingerprintRemoved"])
	}

	warnings := data["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	w := warnings[0].(map[string]any)
	if w["code"] != "access_code_not_found" {
		t.Errorf("warning code = %v, want access_code_not_found", w["code"])
	}
}

func TestRemove_InsecureProfile_NoFingerprintWarning(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", true) // insecure: no fingerprint stored

	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	json.Unmarshal([]byte(out), &env) //nolint:errcheck
	data := env["data"].(map[string]any)
	warnings := data["warnings"].([]any)
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings for insecure profile missing fingerprint, got %d", len(warnings))
	}
}

func TestRemove_JSON_EmptyWarningsArray(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)

	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	json.Unmarshal([]byte(out), &env) //nolint:errcheck
	data := env["data"].(map[string]any)
	warnings, ok := data["warnings"]
	if !ok {
		t.Fatal("warnings key missing from JSON")
	}
	arr, ok := warnings.([]any)
	if !ok {
		t.Fatalf("warnings is %T, want []any", warnings)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty warnings array, got %v", arr)
	}
}

func TestRemove_ConfigFilePath(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes")
	if err != nil {
		t.Fatal(err)
	}

	cfg, _ := config.Open(filepath.Join(dir))
	if _, ok := cfg.GetProfile("myprinter"); ok {
		t.Error("profile still in config file after remove")
	}
}

type trackingKeychain struct {
	inner       *keychain.Mock
	deleteCalls int
}

func (k *trackingKeychain) Get(service, account string) (string, error) {
	return k.inner.Get(service, account)
}

func (k *trackingKeychain) Set(service, account, secret string) error {
	return k.inner.Set(service, account, secret)
}

func (k *trackingKeychain) Delete(service, account string) error {
	k.deleteCalls++
	return k.inner.Delete(service, account)
}

type trackingPrompter struct {
	terminal      bool
	lines         []string
	readLineCalls int
}

func (p *trackingPrompter) IsTerminal() bool { return p.terminal }

func (p *trackingPrompter) ReadHidden(_ string) (string, error) {
	return "", fmt.Errorf("ReadHidden should not be called")
}

func (p *trackingPrompter) ReadLine(_ string) (string, error) {
	p.readLineCalls++
	if len(p.lines) == 0 {
		return "", nil
	}
	line := p.lines[0]
	p.lines = p.lines[1:]
	return line, nil
}

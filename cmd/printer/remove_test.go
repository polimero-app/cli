package printer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":access-code", "testcode")
	if !insecure {
		_ = kc.Set(context.Background(), "polimero", "bambu-lan:"+name+":tls-fingerprint", testFingerprint)
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
	if _, err := kc.Get(context.Background(), "polimero", "bambu-lan:garage-x1c:access-code"); err == nil {
		t.Error("access code still in keychain")
	}
	if _, err := kc.Get(context.Background(), "polimero", "bambu-lan:garage-x1c:tls-fingerprint"); err == nil {
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

func TestRemove_TooManyArgsShowsHumanError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "one", "two", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for too many args, got %v", err)
	}
	if out == "" {
		t.Fatal("expected human error output")
	}
	if want := "Error: expected exactly one profile name, got 2"; !bytes.Contains([]byte(out), []byte(want)) {
		t.Fatalf("expected %q in output, got:\n%s", want, out)
	}
}

func TestRemove_TooManyArgsShowsJSONError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "one", "two", "--yes", "--output", "json")
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

func TestRemove_InvalidOutputFormat_PrintsError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, Lines: []string{"yes"}}
	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "myprinter", "--yes", "--output", "xml")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(out, "must be human or json") {
		t.Errorf("expected error message naming valid --output values, got:\n%s", out)
	}
}

func TestRemove_InvalidOutputFormat_NoArgs_PrintsError(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: true, Lines: []string{"yes"}}
	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p}, "--output", "xml")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit 2, got %v", err)
	}
	if !strings.Contains(out, "must be human or json") {
		t.Errorf("expected error message naming valid --output values, got:\n%s", out)
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
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:access-code") // simulate missing entry

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
	_ = kc.Delete(context.Background(), "polimero", "bambu-lan:myprinter:access-code")

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

func TestRemove_JSON_KeychainErrorIsSanitized(t *testing.T) {
	dir := t.TempDir()
	inner := keychain.NewMock()
	seedProfile(t, dir, inner, "myprinter", false)
	kc := &failingDeleteKeychain{inner: inner}

	out, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes", "--output", "json")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("expected exit 3 for keychain failure, got %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if env["ok"] != false {
		t.Fatalf("expected ok=false, got %v", env["ok"])
	}
	if bytes.Contains([]byte(out), []byte("secret-token")) {
		t.Fatalf("raw keychain detail leaked in output:\n%s", out)
	}
	cfg, cfgErr := config.Open(dir)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if _, ok := cfg.GetProfile("myprinter"); !ok {
		t.Fatal("profile must remain when secret deletion fails")
	}
	if _, getErr := inner.Get(context.Background(), "polimero", "bambu-lan:myprinter:access-code"); getErr != nil {
		t.Fatalf("access code must remain after failed removal: %v", getErr)
	}
}

func TestRemove_SecondSecretDeleteFailureRestoresFirstSecret(t *testing.T) {
	dir := t.TempDir()
	inner := keychain.NewMock()
	seedProfile(t, dir, inner, "myprinter", false)
	kc := &accountFailingDeleteKeychain{
		inner:       inner,
		failAccount: "bambu-lan:myprinter:tls-fingerprint",
	}

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: &tty.Mock{Terminal: false}},
		"myprinter", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("expected exit 3, got %v", err)
	}
	for _, account := range []string{
		"bambu-lan:myprinter:access-code",
		"bambu-lan:myprinter:tls-fingerprint",
	} {
		if _, getErr := inner.Get(context.Background(), "polimero", account); getErr != nil {
			t.Fatalf("secret %q was not restored: %v", account, getErr)
		}
	}
	cfg, cfgErr := config.Open(dir)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if _, ok := cfg.GetProfile("myprinter"); !ok {
		t.Fatal("profile must remain when secret deletion fails")
	}
}

func TestRemove_ConfigSaveFailureRestoresSecrets(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	seedProfile(t, dir, kc, "myprinter", false)

	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{
		KC:       kc,
		Prompter: &tty.Mock{Terminal: false},
		SaveConfig: func(string, *config.Config) error {
			return errors.New("disk unavailable")
		},
	}, "myprinter", "--yes")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("expected exit 1, got %v", err)
	}
	for _, account := range []string{
		"bambu-lan:myprinter:access-code",
		"bambu-lan:myprinter:tls-fingerprint",
	} {
		if _, getErr := kc.Get(context.Background(), "polimero", account); getErr != nil {
			t.Fatalf("secret %q was not restored: %v", account, getErr)
		}
	}
	cfg, cfgErr := config.Open(dir)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	if _, ok := cfg.GetProfile("myprinter"); !ok {
		t.Fatal("profile must remain after config save failure")
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

func (k *trackingKeychain) Get(ctx context.Context, service, account string) (string, error) {
	return k.inner.Get(ctx, service, account)
}

func (k *trackingKeychain) Set(ctx context.Context, service, account, secret string) error {
	return k.inner.Set(ctx, service, account, secret)
}

func (k *trackingKeychain) Delete(ctx context.Context, service, account string) error {
	k.deleteCalls++
	return k.inner.Delete(ctx, service, account)
}

type failingDeleteKeychain struct {
	inner *keychain.Mock
}

func (k *failingDeleteKeychain) Get(ctx context.Context, service, account string) (string, error) {
	return k.inner.Get(ctx, service, account)
}

func (k *failingDeleteKeychain) Set(ctx context.Context, service, account, secret string) error {
	return k.inner.Set(ctx, service, account, secret)
}

func (k *failingDeleteKeychain) Delete(context.Context, string, string) error {
	return fmt.Errorf("dbus failure secret-token")
}

type accountFailingDeleteKeychain struct {
	inner       *keychain.Mock
	failAccount string
}

func (k *accountFailingDeleteKeychain) Get(ctx context.Context, service, account string) (string, error) {
	return k.inner.Get(ctx, service, account)
}

func (k *accountFailingDeleteKeychain) Set(ctx context.Context, service, account, secret string) error {
	return k.inner.Set(ctx, service, account, secret)
}

func (k *accountFailingDeleteKeychain) Delete(ctx context.Context, service, account string) error {
	if account == k.failAccount {
		return errors.New("delete failed")
	}
	return k.inner.Delete(ctx, service, account)
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

func TestRemove_NoArgs_ExitsCode2(t *testing.T) {
	dir := t.TempDir()
	kc := keychain.NewMock()
	p := &tty.Mock{Terminal: false}
	_, err := runRemoveCmd(t, dir, printer.RemoveDeps{KC: kc, Prompter: p})
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Errorf("expected exit 2 for missing name, got %v", err)
	}
}

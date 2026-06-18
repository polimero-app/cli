package printer_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/spf13/cobra"
)

// testRoot builds a minimal Cobra root that mimics the real root:
// persistent --output flag + the printer subcommand.
// Tests use this instead of cmd.NewRoot() to avoid a circular import
// (cmd imports cmd/printer; cmd/printer tests cannot also import cmd).
func testRoot(t *testing.T, configDir string) *cobra.Command {
	t.Helper()
	t.Setenv("POLIMERO_CONFIG_DIR", configDir)
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.AddCommand(printer.Command())
	return root
}

func runList(t *testing.T, configDir string, args ...string) (string, error) {
	t.Helper()
	root := testRoot(t, configDir)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"printer", "list"}, args...))
	err := root.Execute()
	return buf.String(), err
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "polimero.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

const twoProfileYAML = `version: 1
profiles:
  garage-x1c:
    driver: bambu-lan
    host: 192.0.2.10
    serial: 01S09C450100ABC
    timeout: 10s
    insecure: false
    created: 2026-06-13T10:00:00Z
    updated: 2026-06-13T10:00:00Z
  attic-p1s:
    driver: bambu-lan
    host: 192.0.2.11
    serial: 01P00C450100XYZ
    timeout: 15s
    insecure: true
    created: 2026-06-13T11:00:00Z
    updated: 2026-06-13T11:00:00Z
`

// --- Human output tests ---

func TestList_Human_Empty(t *testing.T) {
	out, err := runList(t, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No printer profiles configured.") {
		t.Errorf("expected empty message, got:\n%s", out)
	}
}

func TestList_Human_Header(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	out, err := runList(t, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, col := range []string{"NAME", "DRIVER", "HOST", "SERIAL", "TIMEOUT", "INSECURE"} {
		if !strings.Contains(out, col) {
			t.Errorf("missing column %q in output:\n%s", col, out)
		}
	}
}

func TestList_Human_AlphabeticalOrder(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	out, err := runList(t, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idxAttic := strings.Index(out, "attic-p1s")
	idxGarage := strings.Index(out, "garage-x1c")
	if idxAttic < 0 || idxGarage < 0 {
		t.Fatalf("profiles not found in output:\n%s", out)
	}
	if idxAttic > idxGarage {
		t.Errorf("attic-p1s should appear before garage-x1c:\n%s", out)
	}
}

func TestList_Human_InsecureField(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	out, err := runList(t, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "true") || !strings.Contains(out, "false") {
		t.Errorf("expected both true and false in insecure column:\n%s", out)
	}
}

func TestList_Human_SerialField(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	out, err := runList(t, dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "01S09C450100ABC") {
		t.Errorf("expected serial in output:\n%s", out)
	}
}

// --- JSON output tests ---

func TestList_JSON_Empty(t *testing.T) {
	out, err := runList(t, t.TempDir(), "--output", "json")
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
	profiles := data["profiles"].([]any)
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
	if env["error"] != nil {
		t.Errorf("error = %v, want null", env["error"])
	}
}

func TestList_JSON_TwoProfiles(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	out, err := runList(t, dir, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}

	data := env["data"].(map[string]any)
	profiles := data["profiles"].([]any)
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	first := profiles[0].(map[string]any)
	if first["name"] != "attic-p1s" {
		t.Errorf("profiles[0].name = %v, want attic-p1s (alphabetical)", first["name"])
	}
	if first["insecure"] != true {
		t.Errorf("attic-p1s insecure = %v, want true", first["insecure"])
	}
	if first["serial"] != "01P00C450100XYZ" {
		t.Errorf("attic-p1s serial = %v, want 01P00C450100XYZ", first["serial"])
	}
	second := profiles[1].(map[string]any)
	if second["name"] != "garage-x1c" {
		t.Errorf("profiles[1].name = %v, want garage-x1c", second["name"])
	}
	if second["insecure"] != false {
		t.Errorf("garage-x1c insecure = %v, want false", second["insecure"])
	}
	if second["serial"] != "01S09C450100ABC" {
		t.Errorf("garage-x1c serial = %v, want 01S09C450100ABC", second["serial"])
	}
}

func TestList_JSON_SerialOmittedWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `version: 1
profiles:
  no-serial:
    driver: other-driver
    host: 192.0.2.99
    timeout: 10s
    insecure: false
    created: 2026-06-13T10:00:00Z
    updated: 2026-06-13T10:00:00Z
`)
	out, err := runList(t, dir, "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	data := env["data"].(map[string]any)
	profiles := data["profiles"].([]any)
	p := profiles[0].(map[string]any)
	if _, hasSerial := p["serial"]; hasSerial {
		t.Error("serial should be omitted from JSON when empty")
	}
}

func TestList_JSON_Meta(t *testing.T) {
	out, err := runList(t, t.TempDir(), "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env map[string]any
	json.Unmarshal([]byte(out), &env) //nolint:errcheck
	meta := env["meta"].(map[string]any)
	if meta["command"] != "printer list" {
		t.Errorf("meta.command = %v, want printer list", meta["command"])
	}
	if _, ok := meta["durationMs"]; ok {
		t.Error("meta.durationMs should not be present for printer list")
	}
}

// --- Error tests ---

func TestList_InvalidOutputFormat(t *testing.T) {
	out, err := runList(t, t.TempDir(), "--output", "xml")
	if err == nil {
		t.Fatal("expected error for invalid output format")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 2 {
		t.Errorf("exit code = %d, want 2", exitErr.Code)
	}
	if !strings.Contains(out, "must be human or json") {
		t.Errorf("expected error message naming valid --output values, got:\n%s", out)
	}
}

func TestList_MalformedConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 1\nprofiles:\n  bad: [unclosed\n")

	_, err := runList(t, dir)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 2 {
		t.Errorf("exit code = %d, want 2", exitErr.Code)
	}
}

func TestList_UnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 99\nprofiles: {}\n")

	_, err := runList(t, dir)
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 2 {
		t.Errorf("exit code = %d, want 2", exitErr.Code)
	}
}

func TestList_JSON_ConfigError(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 99\nprofiles: {}\n")

	out, _ := runList(t, dir, "--output", "json")
	if !strings.Contains(out, `"ok"`) {
		t.Fatalf("expected JSON output on error, got:\n%s", out)
	}
	var env map[string]any
	if jsonErr := json.Unmarshal([]byte(out), &env); jsonErr != nil {
		t.Fatalf("invalid JSON on error: %v\n%s", jsonErr, out)
	}
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	if env["error"] == nil {
		t.Error("error should not be null on config failure")
	}
}

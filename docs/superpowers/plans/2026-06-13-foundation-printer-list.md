# Foundation + `printer list` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the repository foundation and implement `polimero printer list` end-to-end with human and JSON output.

**Architecture:** Layered Go CLI — Cobra commands call `internal/config` for YAML I/O and `internal/output` for formatting. All command failures carry exit codes via `internal/apperr`. The command scaffold uses a factory function (`NewRoot`) so integration tests can wire their own root without circular imports. This is Plan 1 of 3. Plan 2 adds `printer add` + `printer remove`. Plan 3 adds `printer status` + `printer tls refresh` + the Bambu LAN driver.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra`, `gopkg.in/yaml.v3`, `log/slog` (stdlib)

---

## File Map

| File | Responsibility |
|---|---|
| `.gitignore` | Go binary and tooling exclusions |
| `.github/workflows/ci.yml` | CI matrix on Linux, macOS, Windows |
| `main.go` | Binary entrypoint, calls `cmd.Execute()` |
| `internal/apperr/apperr.go` | `ExitError` — error type carrying a process exit code |
| `internal/output/output.go` | `Format` type, `ParseFormat`, `Envelope`, `WriteEnvelope` |
| `internal/output/output_test.go` | Unit tests for output |
| `internal/config/config.go` | `Config`, `Profile`, `Open(dir)`, `Load()`, `SortedProfiles()` |
| `internal/config/config_test.go` | Unit tests for config |
| `cmd/root.go` | `NewRoot()` factory, `Execute()` with exit-code handling, `--output` global flag |
| `cmd/printer/printer.go` | `Command()` — the `printer` parent subcommand |
| `cmd/printer/list.go` | `printer list` command implementation |
| `cmd/printer/list_test.go` | Integration tests (uses minimal root, no circular import) |

---

## Task 1: Repository Foundation

**Files:**
- Create: `.gitignore`
- Create: `.github/workflows/ci.yml`
- Create: `main.go`
- Modify: `go.mod` (add dependencies)

- [ ] **Step 1: Create `.gitignore`**

```
# Binary
polimero
polimero.exe

# Test and coverage output
*.test
*.out
coverage.html

# macOS
.DS_Store

# Go workspace (not used in this project)
go.work
go.work.sum

# IDEs
.idea/
.vscode/
```

- [ ] **Step 2: Create `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

jobs:
  ci:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - run: make ci
```

- [ ] **Step 3: Add dependencies**

```bash
go get github.com/spf13/cobra@latest
go get gopkg.in/yaml.v3@latest
go mod tidy
```

Expected: `go.mod` now has `require` entries for cobra and yaml.v3. `go.sum` is generated.

- [ ] **Step 4: Create `main.go`**

```go
package main

import "github.com/polimero-app/cli/cmd"

func main() {
	cmd.Execute()
}
```

- [ ] **Step 5: Verify the module compiles**

```bash
go build ./...
```

Expected: exits 0. No output (nothing to build yet beyond main, which imports `cmd` that doesn't exist — this will fail until Task 5). Skip this step until Task 5 is done; revisit then.

---

## Task 2: Exit-Code Error Type

**Files:**
- Create: `internal/apperr/apperr.go`
- Create: `internal/apperr/apperr_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/apperr/apperr_test.go`:

```go
package apperr_test

import (
	"errors"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
)

func TestNew(t *testing.T) {
	err := apperr.New(2, "validation failed")
	if err.Code != 2 {
		t.Errorf("Code = %d, want 2", err.Code)
	}
	if err.Error() != "validation failed" {
		t.Errorf("Error() = %q, want %q", err.Error(), "validation failed")
	}
}

func TestNewf(t *testing.T) {
	err := apperr.Newf(3, "secret store error: %s", "unavailable")
	if err.Code != 3 {
		t.Errorf("Code = %d, want 3", err.Code)
	}
	if err.Error() != "secret store error: unavailable" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestExitError_IsError(t *testing.T) {
	var err error = apperr.New(1, "failure")
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Error("expected errors.As to match *ExitError")
	}
	if exitErr.Code != 1 {
		t.Errorf("Code = %d, want 1", exitErr.Code)
	}
}

func TestNew_EmptyMessage(t *testing.T) {
	err := apperr.New(1, "")
	if err.Error() == "" {
		t.Error("Error() should not be empty even with empty Msg")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/apperr/...
```

Expected: compile error — package does not exist yet.

- [ ] **Step 3: Implement `internal/apperr/apperr.go`**

```go
package apperr

import "fmt"

// ExitError wraps a command failure with a process exit code.
// Commands return an ExitError to signal the desired os.Exit value.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("exit %d", e.Code)
}

// New returns an ExitError with the given exit code and message.
func New(code int, msg string) *ExitError {
	return &ExitError{Code: code, Msg: msg}
}

// Newf returns an ExitError with a formatted message.
func Newf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/apperr/...
```

Expected: `ok  github.com/polimero-app/cli/internal/apperr`

- [ ] **Step 5: Commit**

```bash
git add internal/apperr/ .gitignore .github/ go.mod go.sum
git commit -m "feat: add repository foundation and apperr package"
```

---

## Task 3: Output Package

**Files:**
- Create: `internal/output/output.go`
- Create: `internal/output/output_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/output/output_test.go`:

```go
package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/polimero-app/cli/internal/output"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input   string
		want    output.Format
		wantErr bool
	}{
		{"human", output.FormatHuman, false},
		{"json", output.FormatJSON, false},
		{"xml", "", true},
		{"JSON", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := output.ParseFormat(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseFormat(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("ParseFormat(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteEnvelope_Success(t *testing.T) {
	var buf bytes.Buffer
	err := output.WriteEnvelope(&buf, output.Envelope{
		OK:    true,
		Data:  map[string]any{"profiles": []any{}},
		Error: nil,
		Meta:  output.Meta{Command: "printer list"},
	})
	if err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	var got map[string]any
	if jsonErr := json.Unmarshal(buf.Bytes(), &got); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jsonErr, buf.String())
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	if got["error"] != nil {
		t.Errorf("error = %v, want null", got["error"])
	}
	meta, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta is not an object")
	}
	if meta["command"] != "printer list" {
		t.Errorf("meta.command = %v, want printer list", meta["command"])
	}
	if _, hasDuration := meta["durationMs"]; hasDuration {
		t.Error("meta.durationMs should be absent for non-network commands")
	}
}

func TestWriteEnvelope_Error(t *testing.T) {
	var buf bytes.Buffer
	err := output.WriteEnvelope(&buf, output.Envelope{
		OK:   false,
		Data: nil,
		Error: &output.ErrDetail{
			Code:    "config_error",
			Message: "failed to load config",
			Details: map[string]any{"path": "/home/user/.config/polimero/polimero.yaml"},
		},
		Meta: output.Meta{Command: "printer list"},
	})
	if err != nil {
		t.Fatalf("WriteEnvelope: %v", err)
	}

	if !strings.Contains(buf.String(), "config_error") {
		t.Errorf("expected code in output:\n%s", buf.String())
	}

	var got map[string]any
	if jsonErr := json.Unmarshal(buf.Bytes(), &got); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v", jsonErr)
	}
	if got["ok"] != false {
		t.Errorf("ok = %v, want false", got["ok"])
	}
	if got["data"] != nil {
		t.Errorf("data = %v, want null", got["data"])
	}
}

func TestWriteEnvelope_WithDuration(t *testing.T) {
	dur := int64(148)
	var buf bytes.Buffer
	output.WriteEnvelope(&buf, output.Envelope{ //nolint:errcheck
		OK:   true,
		Data: map[string]any{},
		Meta: output.Meta{Command: "printer status", DurationMs: &dur},
	})

	var got map[string]any
	json.Unmarshal(buf.Bytes(), &got) //nolint:errcheck
	meta := got["meta"].(map[string]any)
	if meta["durationMs"] != float64(148) {
		t.Errorf("meta.durationMs = %v, want 148", meta["durationMs"])
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/output/...
```

Expected: compile error — package does not exist.

- [ ] **Step 3: Implement `internal/output/output.go`**

```go
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Format controls how command results are rendered.
type Format string

const (
	FormatHuman Format = "human"
	FormatJSON  Format = "json"
)

// ParseFormat validates s and returns the corresponding Format.
// Returns an error for any value other than "human" or "json".
func ParseFormat(s string) (Format, error) {
	switch Format(s) {
	case FormatHuman, FormatJSON:
		return Format(s), nil
	}
	return "", fmt.Errorf("invalid output format %q: must be human or json", s)
}

// Envelope is the stable JSON response structure shared by all commands.
// Every JSON response — success or error — uses this shape.
type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data"`
	Error *ErrDetail `json:"error"`
	Meta  Meta       `json:"meta"`
}

// ErrDetail describes a failure in the JSON envelope.
type ErrDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Meta carries metadata about the command invocation.
// DurationMs is only set for commands that make network calls.
type Meta struct {
	Command    string `json:"command"`
	DurationMs *int64 `json:"durationMs,omitempty"`
}

// WriteEnvelope encodes env as indented JSON followed by a newline to w.
func WriteEnvelope(w io.Writer, env Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}
```

- [ ] **Step 4: Run to verify tests pass**

```bash
go test ./internal/output/...
```

Expected: `ok  github.com/polimero-app/cli/internal/output`

- [ ] **Step 5: Commit**

```bash
git add internal/output/
git commit -m "feat: add output package with JSON envelope and format parsing"
```

---

## Task 4: Config Package (Read-Only)

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

The config package exposes `Open(dir string)` for testability and `Load()` for production use. `Load()` respects the `POLIMERO_CONFIG_DIR` env var so tests can redirect without touching `os.UserConfigDir()`.

- [ ] **Step 1: Write the failing tests**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/config"
)

const twoProfileYAML = `version: 1
profiles:
  garage-x1c:
    driver: bambu-lan
    host: 192.0.2.10
    timeout: 10s
    insecure: false
    created: 2026-06-13T10:00:00Z
    updated: 2026-06-13T10:00:00Z
  attic-p1s:
    driver: bambu-lan
    host: 192.0.2.11
    timeout: 15s
    insecure: true
    created: 2026-06-13T11:00:00Z
    updated: 2026-06-13T11:00:00Z
`

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "polimero.yaml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestOpen_MissingFile(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error for missing file: %v", err)
	}
	if profiles := cfg.SortedProfiles(); len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestOpen_MissingDir(t *testing.T) {
	cfg, err := config.Open(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error for missing dir: %v", err)
	}
	if profiles := cfg.SortedProfiles(); len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestOpen_EmptyProfiles(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 1\nprofiles: {}\n")

	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profiles := cfg.SortedProfiles(); len(profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(profiles))
	}
}

func TestOpen_ValidProfiles(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	profiles := cfg.SortedProfiles()
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(profiles))
	}
	// SortedProfiles must return alphabetical order.
	if profiles[0].Name != "attic-p1s" {
		t.Errorf("profiles[0].Name = %q, want attic-p1s", profiles[0].Name)
	}
	if profiles[1].Name != "garage-x1c" {
		t.Errorf("profiles[1].Name = %q, want garage-x1c", profiles[1].Name)
	}
	if profiles[1].Driver != "bambu-lan" {
		t.Errorf("Driver = %q, want bambu-lan", profiles[1].Driver)
	}
	if profiles[1].Host != "192.0.2.10" {
		t.Errorf("Host = %q, want 192.0.2.10", profiles[1].Host)
	}
	if profiles[1].Insecure != false {
		t.Error("Insecure = true, want false")
	}
	if profiles[0].Insecure != true {
		t.Error("attic-p1s Insecure = false, want true")
	}
}

func TestOpen_Timestamps(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)

	cfg, _ := config.Open(dir)
	profiles := cfg.SortedProfiles()

	want := time.Date(2026, 6, 13, 11, 0, 0, 0, time.UTC)
	if !profiles[0].Created.Equal(want) {
		t.Errorf("attic-p1s Created = %v, want %v", profiles[0].Created, want)
	}
}

func TestOpen_UnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 2\nprofiles: {}\n")

	_, err := config.Open(dir)
	if !errors.Is(err, config.ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestOpen_Malformed(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "version: 1\nprofiles:\n  - bad\n  yaml: [unclosed\n")

	_, err := config.Open(dir)
	if !errors.Is(err, config.ErrMalformed) {
		t.Errorf("expected ErrMalformed, got %v", err)
	}
}

func TestOpen_VersionZero(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "profiles: {}\n") // no version field → defaults to 0

	_, err := config.Open(dir)
	if !errors.Is(err, config.ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion for missing version, got %v", err)
	}
}

func TestLoad_EnvVarOverride(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, twoProfileYAML)
	t.Setenv("POLIMERO_CONFIG_DIR", dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.SortedProfiles()) != 2 {
		t.Errorf("expected 2 profiles via Load(), got %d", len(cfg.SortedProfiles()))
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./internal/config/...
```

Expected: compile error — package does not exist.

- [ ] **Step 3: Implement `internal/config/config.go`**

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

const currentVersion = 1

// Sentinel errors. Use errors.Is to check.
var (
	// ErrUnsupportedVersion is returned when the config file's version field
	// is not equal to the current supported version (1).
	ErrUnsupportedVersion = errors.New("unsupported config schema version")
	// ErrMalformed is returned when the config file cannot be parsed as YAML.
	ErrMalformed = errors.New("malformed config file")
)

// Profile holds the non-secret settings for a named printer.
// Name is populated from the YAML map key after loading; it is not stored in the file.
type Profile struct {
	Name     string    `yaml:"-"`
	Driver   string    `yaml:"driver"`
	Host     string    `yaml:"host"`
	Timeout  string    `yaml:"timeout"`
	Insecure bool      `yaml:"insecure"`
	Created  time.Time `yaml:"created"`
	Updated  time.Time `yaml:"updated"`
}

// Config holds the parsed contents of polimero.yaml.
type Config struct {
	profiles map[string]Profile
}

// configFile mirrors the on-disk YAML structure.
type configFile struct {
	Version  int                `yaml:"version"`
	Profiles map[string]Profile `yaml:"profiles"`
}

// Open loads config from dir/polimero.yaml.
// Returns an empty Config (no profiles, no error) if the file does not exist.
// Returns ErrUnsupportedVersion if the version field is not 1.
// Returns ErrMalformed if the YAML cannot be parsed.
func Open(dir string) (*Config, error) {
	path := filepath.Join(dir, "polimero.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{profiles: make(map[string]Profile)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var f configFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMalformed, err)
	}

	if f.Version != currentVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, f.Version, currentVersion)
	}

	if f.Profiles == nil {
		f.Profiles = make(map[string]Profile)
	}

	return &Config{profiles: f.Profiles}, nil
}

// Load loads config from the default OS config directory.
// The POLIMERO_CONFIG_DIR env var overrides the default path (used in tests).
func Load() (*Config, error) {
	dir := os.Getenv("POLIMERO_CONFIG_DIR")
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, fmt.Errorf("locating config directory: %w", err)
		}
		dir = filepath.Join(base, "polimero")
	}
	return Open(dir)
}

// SortedProfiles returns all profiles sorted alphabetically by name.
// Each returned Profile has its Name field populated.
func (c *Config) SortedProfiles() []Profile {
	names := make([]string, 0, len(c.profiles))
	for name := range c.profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Profile, 0, len(names))
	for _, name := range names {
		p := c.profiles[name]
		p.Name = name
		out = append(out, p)
	}
	return out
}
```

- [ ] **Step 4: Run to verify tests pass**

```bash
go test ./internal/config/...
```

Expected: `ok  github.com/polimero-app/cli/internal/config`

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: add config package with versioned YAML loading"
```

---

## Task 5: Command Scaffold

**Files:**
- Create: `cmd/root.go`
- Create: `cmd/printer/printer.go`

No tests in this task. Verification is a successful `go build` and a working `--help`.

- [ ] **Step 1: Create `cmd/root.go`**

```go
package cmd

import (
	"errors"
	"os"

	"github.com/polimero-app/cli/cmd/printer"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/spf13/cobra"
)

// NewRoot creates and returns a fully wired root command.
// Use this in tests to avoid global state.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "polimero",
		Short:         "CLI for interacting with 3D printers",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().String("output", "human", "output format: human or json")
	root.AddCommand(printer.Command())
	return root
}

// Execute builds and runs the root command, then exits the process.
// Exit codes come from *apperr.ExitError returned by subcommands.
func Execute() {
	root := NewRoot()
	if err := root.Execute(); err != nil {
		var exitErr *apperr.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Create `cmd/printer/printer.go`**

```go
package printer

import "github.com/spf13/cobra"

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(listCommand())
	return cmd
}
```

Note: `listCommand()` does not exist yet — the next step adds it. Add a stub for now so the file compiles:

```go
package printer

import "github.com/spf13/cobra"

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	return cmd
}
```

- [ ] **Step 3: Build and verify help output**

```bash
go build -o polimero .
./polimero --help
```

Expected output:
```
CLI for interacting with 3D printers

Usage:
  polimero [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  help        Help about any command
  printer     Manage 3D printer profiles

Flags:
  -h, --help            help for polimero
      --output string   output format: human or json (default "human")

Use "polimero [command] --help" for more information about a command.
```

- [ ] **Step 4: Clean up build artifact and commit**

```bash
rm polimero
git add cmd/ main.go
git commit -m "feat: add command scaffold with root and printer parent command"
```

---

## Task 6: `printer list` Command

**Files:**
- Create: `cmd/printer/list.go`
- Create: `cmd/printer/list_test.go`
- Modify: `cmd/printer/printer.go` (wire `listCommand()`)

- [ ] **Step 1: Write the failing tests**

Create `cmd/printer/list_test.go`:

```go
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
    timeout: 10s
    insecure: false
    created: 2026-06-13T10:00:00Z
    updated: 2026-06-13T10:00:00Z
  attic-p1s:
    driver: bambu-lan
    host: 192.0.2.11
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
	for _, col := range []string{"NAME", "DRIVER", "HOST", "TIMEOUT", "INSECURE"} {
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
	// garage-x1c insecure=false, attic-p1s insecure=true
	if !strings.Contains(out, "true") || !strings.Contains(out, "false") {
		t.Errorf("expected both true and false in insecure column:\n%s", out)
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
	second := profiles[1].(map[string]any)
	if second["name"] != "garage-x1c" {
		t.Errorf("profiles[1].name = %v, want garage-x1c", second["name"])
	}
	if second["insecure"] != false {
		t.Errorf("garage-x1c insecure = %v, want false", second["insecure"])
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
	_, err := runList(t, t.TempDir(), "--output", "xml")
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
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./cmd/printer/...
```

Expected: compile error — `listCommand` not defined, `list.go` does not exist.

- [ ] **Step 3: Implement `cmd/printer/list.go`**

```go
package printer

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

func listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured printer profiles",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
}

func runList(cmd *cobra.Command, _ []string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, err := output.ParseFormat(formatStr)
	if err != nil {
		return apperr.New(2, err.Error())
	}

	cfg, loadErr := config.Load()
	if loadErr != nil {
		return listWriteError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, loadErr)
	}

	profiles := cfg.SortedProfiles()

	if format == output.FormatJSON {
		return output.WriteEnvelope(cmd.OutOrStdout(), output.Envelope{
			OK:    true,
			Data:  map[string]any{"profiles": toJSONProfiles(profiles)},
			Error: nil,
			Meta:  output.Meta{Command: "printer list"},
		})
	}

	return listWriteHuman(cmd.OutOrStdout(), profiles)
}

// jsonProfile is the per-profile shape in the JSON response.
type jsonProfile struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Host     string `json:"host"`
	Timeout  string `json:"timeout"`
	Insecure bool   `json:"insecure"`
}

func toJSONProfiles(profiles []config.Profile) []jsonProfile {
	out := make([]jsonProfile, len(profiles))
	for i, p := range profiles {
		out[i] = jsonProfile{
			Name:     p.Name,
			Driver:   p.Driver,
			Host:     p.Host,
			Timeout:  p.Timeout,
			Insecure: p.Insecure,
		}
	}
	return out
}

func listWriteHuman(w io.Writer, profiles []config.Profile) error {
	if len(profiles) == 0 {
		_, err := fmt.Fprintln(w, "No printer profiles configured.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDRIVER\tHOST\tTIMEOUT\tINSECURE")
	for _, p := range profiles {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\n", p.Name, p.Driver, p.Host, p.Timeout, p.Insecure)
	}
	return tw.Flush()
}

func listWriteError(out, errOut io.Writer, format output.Format, err error) error {
	code := listExitCode(err)
	msg := sanitizeConfigErr(err)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: "config_error", Message: msg},
			Meta:  output.Meta{Command: "printer list"},
		})
	} else {
		fmt.Fprintf(errOut, "Error: %s\n", msg)
	}
	return apperr.New(code, "")
}

func listExitCode(err error) int {
	if errors.Is(err, config.ErrUnsupportedVersion) || errors.Is(err, config.ErrMalformed) {
		return 2
	}
	return 1
}

func sanitizeConfigErr(err error) string {
	switch {
	case errors.Is(err, config.ErrUnsupportedVersion):
		return "unsupported config schema version"
	case errors.Is(err, config.ErrMalformed):
		return "config file is malformed"
	default:
		return "failed to read config"
	}
}
```

- [ ] **Step 4: Wire `listCommand()` into `printer.go`**

Update `cmd/printer/printer.go` to add the list command:

```go
package printer

import "github.com/spf13/cobra"

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(listCommand())
	return cmd
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
go test ./cmd/printer/...
```

Expected: `ok  github.com/polimero-app/cli/cmd/printer`

- [ ] **Step 6: Run the full test suite and CI targets**

```bash
make ci
```

Expected:
```
ok  github.com/polimero-app/cli/internal/apperr
ok  github.com/polimero-app/cli/internal/config
ok  github.com/polimero-app/cli/internal/output
ok  github.com/polimero-app/cli/cmd/printer
no Go packages yet; skipping lint   ← or lint runs cleanly if golangci-lint is installed
```

- [ ] **Step 7: Manual smoke test**

```bash
go build -o polimero .
./polimero printer list
```

Expected:
```
No printer profiles configured.
```

```bash
./polimero printer list --output json
```

Expected:
```json
{
  "ok": true,
  "data": {
    "profiles": []
  },
  "error": null,
  "meta": {
    "command": "printer list"
  }
}
```

```bash
rm polimero
```

- [ ] **Step 8: Commit**

```bash
git add cmd/printer/
git commit -m "feat: implement printer list command with human and JSON output"
```

---

## Self-Review Checklist

**Spec coverage:**

| Spec requirement | Covered |
|---|---|
| `polimero printer list [--output <format>]` syntax | ✓ Task 6 |
| `--output` is a global flag | ✓ Task 5 (`PersistentFlags`) |
| Profiles returned in alphabetical order | ✓ `SortedProfiles()` + test |
| Empty list → "No printer profiles configured." | ✓ test |
| Human: NAME/DRIVER/HOST/TIMEOUT/INSECURE columns | ✓ `listWriteHuman` |
| JSON: `{"ok", "data.profiles", "error", "meta"}` envelope | ✓ `WriteEnvelope` + test |
| `insecure` field in JSON profile | ✓ `jsonProfile` struct |
| Exit 0 on success | ✓ no error returned |
| Exit 2 on malformed config | ✓ `listExitCode` + test |
| Exit 2 on unsupported schema version | ✓ `listExitCode` + test |
| Exit 2 on invalid `--output` | ✓ `apperr.New(2, ...)` + test |
| Config missing → success with empty list | ✓ `config.Open` + test |
| Does not access keychain | ✓ by construction |
| JSON error envelope on config failure with `--output json` | ✓ `listWriteError` + test |
| `meta.durationMs` absent (not a network command) | ✓ `Meta{}` has no DurationMs set + test |

**Gaps:** None found.

**Placeholder scan:** No TBD/TODO/placeholder language found.

**Type consistency:** `config.Profile` → `jsonProfile` → `output.Envelope` all consistent. `apperr.ExitError` returned by commands, checked by tests via `errors.As`.

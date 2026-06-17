# Promote `status` to a Top-Level Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move `printer status` to a top-level `status` command, with the documentation (ADR + command spec) and code changes that requires.

**Architecture:** Write ADR 0010 superseding ADR 0008's placement of `status`, mark ADR 0008 `Superseded`, and rename/update the `printer-status` command spec. Lift `validateProfileName` out of `cmd/printer` into `internal/config` so it's reachable from outside the `printer` package. Move the command implementation and tests from `cmd/printer` into a new `cmd/status` package, drop the `Status` name stutter now that the package disambiguates, update the `meta.command` JSON field, and wire `cmd/root.go`.

**Tech Stack:** Go 1.26.4, Cobra, standard library `testing`.

---

## File Map

| File | Action |
|---|---|
| `docs/adr/0010-status-top-level-command.md` | Create |
| `docs/adr/0008-top-level-action-commands.md` | Modify — `Status: Superseded`, pointer to 0010 |
| `docs/specs/commands/printer-status.md` | Rename to `docs/specs/commands/status.md`, update syntax/title/`meta.command` |
| `internal/config/config.go` | Modify — add `ValidateProfileName` |
| `internal/config/config_test.go` | Modify — add tests for `ValidateProfileName` |
| `cmd/printer/add.go` | Modify — remove `validateProfileName`/`profileNameRE`, call `config.ValidateProfileName` |
| `cmd/printer/remove.go` | Modify — call `config.ValidateProfileName` |
| `cmd/printer/tls_refresh.go` | Modify — call `config.ValidateProfileName` |
| `cmd/printer/status.go` | Delete (moved) |
| `cmd/printer/status_test.go` | Delete (moved) |
| `cmd/printer/printer.go` | Modify — drop `statusCommand()` registration |
| `cmd/status/status.go` | Create — moved from `cmd/printer/status.go`, package renamed, `Status` stutter dropped, `meta.command` updated |
| `cmd/status/status_test.go` | Create — moved from `cmd/printer/status_test.go`, adjusted imports/names |
| `cmd/root.go` | Modify — wire `status.Command()` |
| `README.md` | Modify — update command list and example invocation |
| `docs/specs/drivers/bambu-lan.md` | Modify — update command name in purpose statement and command support list |

---

## Task 1: Extract `ValidateProfileName` into `internal/config`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestValidateProfileName_Valid(t *testing.T) {
	for _, name := range []string{"a", "garage-x1c", "Attic.P1S", "a_b-c.99"} {
		if err := config.ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateProfileName_Empty(t *testing.T) {
	if err := config.ValidateProfileName(""); err == nil {
		t.Error("expected error for empty name, got nil")
	}
}

func TestValidateProfileName_TooLong(t *testing.T) {
	name := strings.Repeat("a", 65)
	if err := config.ValidateProfileName(name); err == nil {
		t.Error("expected error for 65-char name, got nil")
	}
}

func TestValidateProfileName_InvalidChars(t *testing.T) {
	for _, name := range []string{"_leading-underscore", "-leading-dash", "has space", "has/slash"} {
		if err := config.ValidateProfileName(name); err == nil {
			t.Errorf("ValidateProfileName(%q) = nil, want error", name)
		}
	}
}
```

Add `"strings"` to the import block in `internal/config/config_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run TestValidateProfileName -v`
Expected: FAIL with `undefined: config.ValidateProfileName`

- [ ] **Step 3: Implement `ValidateProfileName`**

In `internal/config/config.go`, add `"regexp"` to the import block (alphabetical order, between `"path/filepath"` and `"sort"`), then add near the top of the file (after the `var` block containing `ErrUnsupportedVersion`/`ErrMalformed`):

```go
var profileNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateProfileName checks that name is non-empty, at most 64 characters,
// and contains only ASCII letters, digits, '.', '_', '-', starting with a
// letter or digit.
func ValidateProfileName(name string) error {
	if name == "" {
		return errors.New("profile name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("profile name too long (max 64 chars): %q", name)
	}
	if !profileNameRE.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use only ASCII letters, digits, '.', '_', '-', starting with a letter or digit", name)
	}
	return nil
}
```

This intentionally returns plain `errors`/`fmt` errors, not `*apperr.ExitError` — `internal/config` has no dependency on `internal/apperr` today, and adding one for a single validator isn't justified. Callers in `cmd/printer` already wrap config errors into exit-code-2 `apperr` errors at the call site (see Task 2), so the exit-code mapping stays in the command layer where it already lives for every other config error.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -run TestValidateProfileName -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add ValidateProfileName"
```

---

## Task 2: Point `cmd/printer` call sites at `config.ValidateProfileName`

**Files:**
- Modify: `cmd/printer/add.go`
- Modify: `cmd/printer/remove.go`
- Modify: `cmd/printer/tls_refresh.go`

- [ ] **Step 1: Remove the local helper and regex from `add.go`, wrap the call site**

In `cmd/printer/add.go`, delete the `profileNameRE` var (line 23) and the `validateProfileName` function (lines 339-350):

```go
func validateProfileName(name string) error {
	if name == "" {
		return apperr.New(2, "profile name is required")
	}
	if len(name) > 64 {
		return apperr.Newf(2, "profile name too long (max 64 chars): %q", name)
	}
	if !profileNameRE.MatchString(name) {
		return apperr.Newf(2, "invalid profile name %q: use only ASCII letters, digits, '.', '_', '-', starting with a letter or digit", name)
	}
	return nil
}
```

Remove `"regexp"` from the import block only if `dnsLabelRE` (still used by `validateHost`) is the sole remaining user — it isn't, `dnsLabelRE` still needs `regexp`, so leave the import.

Change the call site in `doAdd`:

```go
	if err := validateProfileName(name); err != nil {
		return err
	}
```

to:

```go
	if err := config.ValidateProfileName(name); err != nil {
		return apperr.New(2, err.Error())
	}
```

- [ ] **Step 2: Update `remove.go`**

In `cmd/printer/remove.go`, change in `doRemove`:

```go
	if err := validateProfileName(name); err != nil {
		return err
	}
```

to:

```go
	if err := config.ValidateProfileName(name); err != nil {
		return apperr.New(2, err.Error())
	}
```

`config` is already imported in this file.

- [ ] **Step 3: Update `tls_refresh.go`**

In `cmd/printer/tls_refresh.go`, change in `doTlsRefresh`:

```go
	if err := validateProfileName(name); err != nil {
		return "", 0, "", err
	}
```

to:

```go
	if err := config.ValidateProfileName(name); err != nil {
		return "", 0, "", apperr.New(2, err.Error())
	}
```

`config` is already imported in this file.

- [ ] **Step 4: Run the existing test suite for these three commands**

Run: `go test ./cmd/printer/... -run 'TestAdd|TestRemove|TestTlsRefresh' -v`
Expected: PASS (behavior unchanged — same exit code 2, same message text, since `err.Error()` reproduces the original message verbatim)

- [ ] **Step 5: Commit**

```bash
git add cmd/printer/add.go cmd/printer/remove.go cmd/printer/tls_refresh.go
git commit -m "refactor(printer): use config.ValidateProfileName"
```

---

## Task 3: Move the status command to `cmd/status`

**Files:**
- Create: `cmd/status/status.go`
- Delete: `cmd/printer/status.go`
- Modify: `cmd/printer/printer.go`

- [ ] **Step 1: Create `cmd/status/status.go`**

This is `cmd/printer/status.go` with: `package printer` → `package status`; `validateProfileName(name)` → `config.ValidateProfileName(name)` (wrapped the same way as Task 2, since this file no longer has access to the package-local helper either — it never did, it's being extracted at the same time the file moves); `StatusDeps` → `Deps`; `StatusCommandWithDeps` → `CommandWithDeps`; `statusCommand()` folded into an exported `Command()`; `Meta.Command` value `"printer status"` → `"status"` (two occurrences, in `writeStatusSuccess` and `writeStatusError`).

```go
package status

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/spf13/cobra"
)

// Deps holds injectable dependencies for the status command.
type Deps struct {
	KC        keychain.Keychain
	GetDriver func(string) (driver.Driver, bool)
	Log       *slog.Logger
}

// Command returns the "status" cobra command with real dependencies wired.
func Command() *cobra.Command {
	return CommandWithDeps(Deps{
		KC:        keychain.NewReal(),
		GetDriver: drivers.Get,
		Log:       slog.Default(),
	})
}

// CommandWithDeps constructs the "status" cobra command with injected dependencies.
func CommandWithDeps(deps Deps) *cobra.Command {
	var flags struct {
		timeout  string
		insecure bool
	}

	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show the current status of a printer",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return writeStatusUsageError(cmd, "profile name is required")
			}
			if len(args) > 1 {
				return writeStatusUsageError(cmd, fmt.Sprintf("expected exactly one profile name, got %d", len(args)))
			}
			return runStatus(cmd, args[0], flags.timeout, flags.insecure, deps)
		},
	}
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "override the profile connection timeout (e.g. 10s)")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS fingerprint verification for this invocation")
	return cmd
}

func writeStatusUsageError(cmd *cobra.Command, message string) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}
	return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, apperr.New(2, message), statusErrorContext{})
}

func runStatus(cmd *cobra.Command, nameArg, timeoutFlag string, insecureFlag bool, deps Deps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	name := strings.ToLower(nameArg)
	verboseFlag, _ := cmd.Root().PersistentFlags().GetBool("verbose")
	verbose := verboseFlag && format == output.FormatHuman
	result, durationMs, driverName, errCtx, err := doStatus(cmd, name, timeoutFlag, insecureFlag, verbose, deps)
	if err != nil {
		return writeStatusError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err, errCtx)
	}
	return writeStatusSuccess(cmd.OutOrStdout(), format, name, driverName, result, durationMs)
}

type statusErrorContext struct {
	profile string
	timeout string
}

func doStatus(cmd *cobra.Command, name, timeoutFlag string, insecureFlag, verbose bool, deps Deps) (*driver.StatusResult, int64, string, statusErrorContext, error) {
	if err := config.ValidateProfileName(name); err != nil {
		return nil, 0, "", statusErrorContext{}, apperr.New(2, err.Error())
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return nil, 0, "", statusErrorContext{}, apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return nil, 0, "", statusErrorContext{}, apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return nil, 0, "", statusErrorContext{}, apperr.Newf(2, "printer profile %q not found", name)
	}

	timeoutStr := p.Timeout
	if timeoutFlag != "" {
		timeoutStr = timeoutFlag
	}
	if timeoutStr == "" {
		timeoutStr = "10s"
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return nil, 0, "", statusErrorContext{profile: name, timeout: timeoutStr}, apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}
	if timeout <= 0 {
		return nil, 0, "", statusErrorContext{profile: name, timeout: timeoutStr}, apperr.Newf(2, "--timeout must be greater than zero")
	}
	errCtx := statusErrorContext{profile: name, timeout: timeout.String()}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	insecure := p.Insecure || insecureFlag

	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	accessCode, err := deps.KC.Get(ctx, "polimero", kcAcct)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return nil, 0, "", errCtx, apperr.Newf(3, "access code not found in keychain for %q", name)
		}
		return nil, 0, "", errCtx, apperr.Wrap(3, "cannot read access code from keychain", err)
	}

	var tlsFingerprint string
	if !insecure {
		kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
		tlsFingerprint, err = deps.KC.Get(ctx, "polimero", kcFpAcct)
		if err != nil {
			if errors.Is(err, keychain.ErrNotFound) {
				return nil, 0, "", errCtx, apperr.Newf(3, "TLS fingerprint not found in keychain for %q", name)
			}
			return nil, 0, "", errCtx, apperr.Wrap(3, "cannot read TLS fingerprint from keychain", err)
		}
		if !driver.ValidTLSFingerprint(tlsFingerprint) {
			return nil, 0, "", errCtx, apperr.Newf(3, "invalid TLS fingerprint in keychain for %q", name)
		}
	}

	drv, ok := deps.GetDriver(p.Driver)
	if !ok {
		return nil, 0, "", errCtx, apperr.Newf(2, "unknown driver %q", p.Driver)
	}
	if !drv.Capabilities().Status {
		return nil, 0, "", errCtx, apperr.Newf(5, "driver %q does not support the status command", p.Driver)
	}

	pi := driver.ProfileInput{
		Name:     name,
		Driver:   p.Driver,
		Host:     p.Host,
		Serial:   p.Serial,
		Timeout:  timeout,
		Insecure: insecure,
	}
	secrets := driver.SecretsBundle{
		AccessCode:     accessCode,
		TLSFingerprint: tlsFingerprint,
	}

	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("Connecting to %s:8883...", p.Host))
	start := time.Now()
	result, err := drv.Status(ctx, pi, secrets, deps.Log)
	durationMs := time.Since(start).Milliseconds()
	if err != nil {
		return nil, 0, "", errCtx, err
	}
	output.Verbose(cmd.OutOrStdout(), verbose, fmt.Sprintf("Response received (%dms).", durationMs))
	return result, durationMs, p.Driver, statusErrorContext{}, nil
}

func writeStatusSuccess(w io.Writer, format output.Format, name, driverName string, result *driver.StatusResult, durationMs int64) error {
	if format == output.FormatJSON {
		dm := durationMs
		type statusData struct {
			Profile string `json:"profile"`
			Driver  string `json:"driver"`
			*driver.StatusResult
		}
		data := statusData{
			Profile:      name,
			Driver:       driverName,
			StatusResult: result,
		}
		return output.WriteEnvelope(w, output.Envelope{
			OK:    true,
			Data:  data,
			Error: nil,
			Meta:  output.Meta{Command: "status", DurationMs: &dm},
		})
	}
	lines := []string{
		fmt.Sprintf("Printer: %s", name),
		fmt.Sprintf("State: %s", result.State),
	}
	if result.Progress != nil {
		lines = append(lines, fmt.Sprintf("Progress: %d%%", result.Progress.Percent))
	}
	if result.Temperatures != nil {
		if n := result.Temperatures.Nozzle; n != nil {
			if n.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f C / %.1f C", n.CurrentCelsius, *n.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Nozzle: %.1f C", n.CurrentCelsius))
			}
		}
		if b := result.Temperatures.Bed; b != nil {
			if b.TargetCelsius != nil {
				lines = append(lines, fmt.Sprintf("Bed: %.1f C / %.1f C", b.CurrentCelsius, *b.TargetCelsius))
			} else {
				lines = append(lines, fmt.Sprintf("Bed: %.1f C", b.CurrentCelsius))
			}
		}
		if c := result.Temperatures.Chamber; c != nil {
			lines = append(lines, fmt.Sprintf("Chamber: %.1f C", c.CurrentCelsius))
		}
	}
	if result.Job != nil {
		lines = append(lines, fmt.Sprintf("Job: %s", result.Job.Name))
	}
	if len(result.Errors) > 0 {
		lines = append(lines, "Errors:")
		for _, statusErr := range result.Errors {
			if statusErr.Code != "" {
				lines = append(lines, fmt.Sprintf("- %s %s", statusErr.Code, statusErr.Message))
			} else {
				lines = append(lines, fmt.Sprintf("- %s", statusErr.Message))
			}
		}
	}
	if len(result.Warnings) > 0 {
		lines = append(lines, "Warnings:")
		for _, warn := range result.Warnings {
			lines = append(lines, fmt.Sprintf("- %s", warn.Message))
		}
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeStatusError(out, errOut io.Writer, format output.Format, err error, errCtx statusErrorContext) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	errDetail := statusErrorDetail(err, errCtx)
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &errDetail,
			Meta:  output.Meta{Command: "status"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", errDetail.Message)
	}
	return apperr.New(code, "")
}

func statusErrorDetail(err error, errCtx statusErrorContext) output.ErrDetail {
	detail := output.ErrDetail{Code: statusErrorCode(err), Message: statusErrorMessage(err)}
	if isStatusTimeout(err) {
		detail.Code = "timeout"
		detail.Message = "printer status request timed out"
		if errCtx.profile != "" || errCtx.timeout != "" {
			detail.Details = map[string]any{}
			if errCtx.profile != "" {
				detail.Details["profile"] = errCtx.profile
			}
			if errCtx.timeout != "" {
				detail.Details["timeout"] = errCtx.timeout
			}
		}
	}
	return detail
}

func statusErrorMessage(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch statusErrorCode(err) {
	case "auth_error":
		switch {
		case strings.Contains(msg, "MQTT authentication rejected"):
			return "MQTT authentication rejected"
		case strings.Contains(msg, "TLS fingerprint mismatch"):
			return "TLS fingerprint mismatch"
		case strings.Contains(lower, "keychain"):
			return msg
		default:
			return "authentication or secret error"
		}
	case "network_error":
		switch {
		case strings.Contains(lower, "cancelled"):
			return "printer status request cancelled"
		case strings.Contains(msg, "invalid status report"):
			return "invalid status report"
		case strings.Contains(msg, "status subscription failed"):
			return "status subscription failed"
		case strings.Contains(msg, "status request failed"):
			return "status request failed"
		case strings.Contains(msg, "connection failed"):
			return "connection failed"
		default:
			return "printer status request failed"
		}
	default:
		return msg
	}
}

func statusErrorCode(err error) string {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		return "error"
	}
	switch exitErr.Code {
	case 2:
		return "config_error"
	case 3:
		return "auth_error"
	case 4:
		return "network_error"
	case 5:
		return "capability_unsupported"
	default:
		return "error"
	}
}

func isStatusTimeout(err error) bool {
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 4 {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timed out") || strings.Contains(msg, "timeout")
}
```

Note the error message text itself (`"printer status request timed out"`, `"printer status request failed"`, etc.) is left unchanged — those are human-facing strings describing what the command does, not the command's invocation path, and the command spec's `Error Cases`/`Output` sections don't require them to change.

- [ ] **Step 2: Delete `cmd/printer/status.go`**

```bash
rm cmd/printer/status.go
```

- [ ] **Step 3: Drop the registration in `cmd/printer/printer.go`**

Remove this line from `Command()`:

```go
	cmd.AddCommand(statusCommand())
```

- [ ] **Step 4: Verify the non-test package still builds (the test package is expected to fail until Task 4)**

Run: `go build ./cmd/printer/...`
Expected: PASS — `go build` does not compile `_test.go` files, so the still-present `cmd/printer/status_test.go` (referencing the now-removed `printer.StatusDeps`/`printer.StatusCommandWithDeps`) doesn't block this.

Run: `go vet ./cmd/printer/...`
Expected: FAIL — `go vet` does compile test files, and will report `undefined: printer.StatusDeps` (or similar) from `status_test.go`. This is expected; Task 4 moves the test file. Do not commit yet.

---

## Task 4: Move the status command tests to `cmd/status`

**Files:**
- Create: `cmd/status/status_test.go`
- Delete: `cmd/printer/status_test.go`

- [ ] **Step 1: Create `cmd/status/status_test.go`**

This is `cmd/printer/status_test.go` with: `package printer_test` → `package status_test`; import `"github.com/polimero-app/cli/cmd/status"` instead of `cmd/printer`; `printer.StatusDeps` → `status.Deps`; `printer.StatusCommandWithDeps` → `status.CommandWithDeps`; the test root no longer wraps the command in a `printer` sub-command (it's top-level now); `meta["command"]` expectations change from `"printer status"` to `"status"`; and it needs its own copies of `testFingerprint` and `seedProfile`, since it can no longer reach `printer_test`'s `test_constants_test.go` or `remove_test.go`.

```go
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

func statusDeps(t *testing.T, dir string, kc *keychain.Mock, drv driver.Driver) status.Deps {
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

func testRootForStatus(deps status.Deps) *cobra.Command {
	root := &cobra.Command{Use: "polimero", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().String("output", "human", "")
	root.PersistentFlags().Bool("verbose", false, "")
	root.AddCommand(status.CommandWithDeps(deps))
	return root
}

func runStatusCmd(t *testing.T, deps status.Deps, args ...string) (string, error) {
	t.Helper()
	root := testRootForStatus(deps)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs(append([]string{"status"}, args...))
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
```

- [ ] **Step 2: Delete `cmd/printer/status_test.go`**

```bash
rm cmd/printer/status_test.go
```

- [ ] **Step 3: Run the full build and test suite**

Run: `go build ./... && go test ./cmd/printer/... ./cmd/status/... ./internal/config/... -v`
Expected: PASS — `cmd/printer` builds and its remaining tests pass; `cmd/status` is a new package whose tests all pass; `internal/config` passes including the new `ValidateProfileName` tests.

- [ ] **Step 4: Commit**

```bash
git add cmd/status/status.go cmd/status/status_test.go cmd/printer/status.go cmd/printer/status_test.go cmd/printer/printer.go
git commit -m "feat(status): move status to a top-level command"
```

---

## Task 5: Wire `cmd/status` into the root command

**Files:**
- Modify: `cmd/root.go`

- [ ] **Step 1: Add the import and registration**

In `cmd/root.go`, add `"github.com/polimero-app/cli/cmd/status"` to the import block (alphabetically after `"github.com/polimero-app/cli/cmd/printer"`), and add this line in `NewRoot()` after `root.AddCommand(printer.Command())`:

```go
	root.AddCommand(status.Command())
```

- [ ] **Step 2: Manually verify the command tree**

Run: `go run . status --help`
Expected: usage text for `status <name> [flags]`, not a cobra "unknown command" error.

Run: `go run . printer status --help`
Expected: cobra "unknown command \"status\" for \"polimero printer\"" error — confirms the old path is gone, per the no-alias decision.

- [ ] **Step 3: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: PASS, all packages.

- [ ] **Step 4: Commit**

```bash
git add cmd/root.go
git commit -m "feat(status): wire status command into root"
```

---

## Task 6: Write ADR 0010 and supersede ADR 0008

**Files:**
- Create: `docs/adr/0010-status-top-level-command.md`
- Modify: `docs/adr/0008-top-level-action-commands.md`

- [ ] **Step 1: Create `docs/adr/0010-status-top-level-command.md`**

```markdown
# ADR 0010: Promote `status` to a Top-Level Command

## Status

Accepted

Supersedes ADR 0008's placement of `status` under `printer`.

## Context

ADR 0008 split the CLI into a `printer` management plane and a top-level operational plane for commands that act on a running printer, but kept `status` under `printer` rather than moving it to the new operational plane — most likely because `status` was the first command implemented, before the plane split existed.

`status` queries the live printer over the network, not the local profile store. By ADR 0008's own test for top-level placement — "operations against a running printer" — `status` belongs in the operational plane alongside `camera`, not in the management plane alongside `add`/`remove`/`list`. The mismatch also created ergonomic and consistency friction: `status` is the single most-used command and had the longest invocation path, and it was the only "talk to a live printer" command not living at the top level once `camera` was added.

The project has not shipped a release (no tags, no changelog), so there is no installed base to preserve compatibility for.

## Decision

`status` moves to the top level: `polimero status <name>`. `printer status` no longer exists; there is no alias and no deprecation period.

- `printer` owns: `add`, `remove`, `list`, `discover`, `tls-refresh`, `drivers`.
- Top level owns: `status`, `camera`, and future groups such as `jobs` and `files`.

This otherwise carries forward ADR 0008 unchanged: every top-level command takes a `<name>` positional argument that resolves a printer profile via the same config and secret loading path; every new top-level group still requires a command spec before implementation; capability checks and exit code `5` for unsupported capabilities are unchanged.

## Consequences

- `printer` is now exclusively a profile-management plane; every command under it reads or writes the local profile store, never the live printer.
- The top-level operational plane is now consistent in shape: `status`, `camera stream`, and future `jobs`/`files` commands are all "verb/group + `<name>`" against a live printer.
- `cmd/printer`'s `validateProfileName` helper moved to `internal/config.ValidateProfileName` so it remains reachable from the new `cmd/status` package and from future top-level groups, without those packages importing from `cmd/printer`.
```

- [ ] **Step 2: Update `docs/adr/0008-top-level-action-commands.md`**

Change line 5 from:

```markdown
Accepted
```

to:

```markdown
Superseded by [ADR 0010](0010-status-top-level-command.md).
```

Leave the rest of the file unchanged — it remains the historical record of the original decision.

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0010-status-top-level-command.md docs/adr/0008-top-level-action-commands.md
git commit -m "docs(adr): supersede ADR 0008, add ADR 0010 promoting status to top level"
```

---

## Task 7: Rename and update the command spec

**Files:**
- Rename: `docs/specs/commands/printer-status.md` → `docs/specs/commands/status.md`

- [ ] **Step 1: Rename the file**

```bash
git mv docs/specs/commands/printer-status.md docs/specs/commands/status.md
```

- [ ] **Step 2: Update the title and syntax**

Change line 1 from:

```markdown
# Command Spec: `printer status`
```

to:

```markdown
# Command Spec: `status`
```

Change the `Syntax` code block (lines 13-15) from:

```text
polimero printer status <name> [--timeout <duration>] [--insecure] [--output <format>]
```

to:

```text
polimero status <name> [--timeout <duration>] [--insecure] [--output <format>]
```

- [ ] **Step 3: Update `meta.command` in both JSON examples**

In the "JSON success example" block, change:

```json
  "meta": {
    "command": "printer status",
    "durationMs": 148
  }
```

to:

```json
  "meta": {
    "command": "status",
    "durationMs": 148
  }
```

In the "JSON timeout example" block, change:

```json
  "meta": {
    "command": "printer status"
  }
```

to:

```json
  "meta": {
    "command": "status"
  }
```

- [ ] **Step 4: Commit**

```bash
git add docs/specs/commands/status.md
git commit -m "docs(status): rename printer-status spec to status"
```

---

## Task 8: Update other living docs that name the old path

ADR 0006, ADR 0007, and ADR 0008 also mention `printer status`, but those mentions are incidental detail in historical decision records (driver scope, TOFU/insecure behavior) — consistent with leaving ADR 0008's body untouched in Task 6, they are not edited here. `README.md` and `docs/specs/drivers/bambu-lan.md` are different: they're living, current-state documents (the README's "Current Status"/"Key Decisions" sections, and an Accepted driver spec listing actual command support), not records of a past decision, so they need to stay accurate.

**Files:**
- Modify: `README.md`
- Modify: `docs/specs/drivers/bambu-lan.md`

- [ ] **Step 1: Update `README.md`**

Change line 11 from:

```markdown
Implemented commands currently include `printer add`, `printer list`, `printer remove`, `printer drivers`, and `printer status`.
```

to:

```markdown
Implemented commands currently include `printer add`, `printer list`, `printer remove`, `printer drivers`, and `status`.
```

Change line 20 from:

```markdown
- First read command: `polimero printer status <name>`
```

to:

```markdown
- First read command: `polimero status <name>`
```

- [ ] **Step 2: Update `docs/specs/drivers/bambu-lan.md`**

Change line 9 from:

```markdown
Define the Bambu LAN driver slice for printer status, discovery, TLS refresh, and device file management.
```

to:

```markdown
Define the Bambu LAN driver slice for status, discovery, TLS refresh, and device file management.
```

Change line 25 (under "Initial command support:") from:

```markdown
- `printer status`
```

to:

```markdown
- `status`
```

- [ ] **Step 3: Commit**

```bash
git add README.md docs/specs/drivers/bambu-lan.md
git commit -m "docs: update README and bambu-lan spec for top-level status command"
```

---

## Task 9: Final verification

**Files:** none (verification only)

- [ ] **Step 1: Full build and test run**

Run: `go build ./... && go test ./...`
Expected: PASS, no failures, no packages skipped.

- [ ] **Step 2: Confirm `cmd/printer` no longer contains the status command**

Run: `grep -rn "statusCommand\|StatusDeps\|StatusCommandWithDeps" cmd/printer/*.go`
Expected: no matches. (A bare `grep "status"` is too broad here — `add_test.go`, `discover_test.go`, and `tls_refresh_test.go` legitimately reference a `Status` field on `driver.Capabilities` and a `Status()` stub method as part of satisfying the unrelated `driver.Driver` interface; those are expected to remain.)

- [ ] **Step 3: Confirm no remaining reference to the old spec filename or ADR status text**

Run: `grep -rln "printer-status" docs/adr docs/specs ; grep -n "^Accepted$" docs/adr/0008-top-level-action-commands.md`

Scope is deliberately `docs/adr` and `docs/specs` only, not all of `docs/` — `docs/superpowers/plans` and `docs/superpowers/specs` are historical planning records (including this very plan and the 2026-06-14 `plan3-printer-status` design/plan from when the command was first built) and are expected to keep mentioning the old name in prose; they're not rewritten.

Expected: first command has no output (no references to the old filename remain in ADRs or command specs); second command has no output (0008 no longer says bare "Accepted").

- [ ] **Step 4: Confirm the living docs no longer say "printer status"**

Run: `grep -n "printer status" README.md docs/specs/drivers/bambu-lan.md`
Expected: no output — both were updated in Task 8. (ADR 0006, ADR 0007, and ADR 0008 are expected to still contain "printer status" in their historical-record prose; this check intentionally does not include them.)

- [ ] **Step 5: Manual smoke test**

Run: `go run . status --help` and `go run . printer --help`
Expected: `status --help` shows the moved command's flags; `printer --help` no longer lists `status` among its subcommands.

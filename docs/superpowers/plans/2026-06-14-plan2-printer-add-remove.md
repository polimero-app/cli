# Plan 2: `printer add` and `printer remove` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `printer add` and `printer remove` commands with bambu-lan TLS+MQTT connectivity check, OS keychain integration, and TOFU fingerprint pinning.

**Architecture:** Eight new packages provide isolated building blocks: `internal/keychain` (OS secret store abstraction), `internal/tty` (terminal prompt abstraction), `internal/driver` (driver interface), `internal/drivers` (registry + bambu-lan implementation). The command layer (`cmd/printer/add.go`, `cmd/printer/remove.go`) wires these together without owning any business logic itself.

**Tech Stack:** Go 1.26.4, `github.com/zalando/go-keyring` (OS keychain), `github.com/eclipse/paho.mqtt.golang` (MQTT over TLS), `golang.org/x/term` (hidden terminal input), existing `gopkg.in/yaml.v3` and `github.com/spf13/cobra`.

**Specs:** `docs/specs/commands/printer-add.md`, `docs/specs/commands/printer-remove.md`, `docs/specs/drivers/bambu-lan.md`, `docs/adr/0007-tls-trust-on-first-use.md`.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `go.mod` / `go.sum` | Modify | Add three new deps |
| `internal/keychain/keychain.go` | Create | `Keychain` interface + `ErrNotFound` |
| `internal/keychain/mock.go` | Create | In-memory mock for tests |
| `internal/keychain/real.go` | Create | `go-keyring` wrapper |
| `internal/keychain/keychain_test.go` | Create | Mock behaviour tests |
| `internal/tty/tty.go` | Create | `Prompter` interface |
| `internal/tty/mock.go` | Create | Preset-value mock for tests |
| `internal/tty/real.go` | Create | `term.ReadPassword` + buffered line reader |
| `internal/config/config.go` | Modify | Add `ConfigDir`, `GetProfile`, `AddProfile`, `RemoveProfile`, `Save`; refactor `Load` |
| `internal/config/config_write_test.go` | Create | Tests for write operations |
| `internal/driver/driver.go` | Create | `Driver` interface (Plan 2 surface only) |
| `internal/drivers/registry.go` | Create | `Get(name)` and `Names()` |
| `internal/drivers/registry_test.go` | Create | Registry lookup tests |
| `internal/drivers/bambulan/bambulan.go` | Create | `ConnectCheck`: TLS + MQTT CONNECT + fingerprint |
| `internal/drivers/bambulan/bambulan_test.go` | Create | Fast-path and network-error tests |
| `cmd/printer/add.go` | Create | `printer add` command with dep injection |
| `cmd/printer/add_test.go` | Create | Add command tests (mock deps) |
| `cmd/printer/remove.go` | Create | `printer remove` command with dep injection |
| `cmd/printer/remove_test.go` | Create | Remove command tests (mock deps) |
| `cmd/printer/printer.go` | Modify | Wire `add` and `remove` into `Command()` |

---

## Task 1: Add dependencies

**Files:** Modify `go.mod`, `go.sum`

- [ ] **Step 1: Add the three new modules**

```bash
go get github.com/zalando/go-keyring
go get github.com/eclipse/paho.mqtt.golang
go get golang.org/x/term
go mod tidy
```

- [ ] **Step 2: Verify go.mod contains the new requires**

```bash
grep -E 'go-keyring|paho|golang.org/x/term' go.mod
```

Expected: three matching lines in the `require` block.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add go-keyring, paho.mqtt.golang, and x/term dependencies"
```

---

## Task 2: `internal/keychain` — interface, mock, real

**Files:**
- Create: `internal/keychain/keychain.go`
- Create: `internal/keychain/mock.go`
- Create: `internal/keychain/real.go`
- Create: `internal/keychain/keychain_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/keychain/keychain_test.go`:

```go
package keychain_test

import (
	"errors"
	"testing"

	"github.com/polimero-app/cli/internal/keychain"
)

func TestMock_SetGetDelete(t *testing.T) {
	kc := keychain.NewMock()

	if _, err := kc.Get("svc", "acc"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound on empty store, got %v", err)
	}

	if err := kc.Set("svc", "acc", "secret"); err != nil {
		t.Fatal(err)
	}

	v, err := kc.Get("svc", "acc")
	if err != nil {
		t.Fatal(err)
	}
	if v != "secret" {
		t.Errorf("got %q, want %q", v, "secret")
	}

	if err := kc.Delete("svc", "acc"); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.Get("svc", "acc"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestMock_DeleteNotFound(t *testing.T) {
	kc := keychain.NewMock()
	if err := kc.Delete("svc", "missing"); !errors.Is(err, keychain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMock_ServiceIsolation(t *testing.T) {
	kc := keychain.NewMock()
	_ = kc.Set("svc1", "acc", "s1")
	_ = kc.Set("svc2", "acc", "s2")
	v1, _ := kc.Get("svc1", "acc")
	v2, _ := kc.Get("svc2", "acc")
	if v1 != "s1" || v2 != "s2" {
		t.Errorf("service isolation broken: got %q, %q", v1, v2)
	}
}

func TestMock_AccountIsolation(t *testing.T) {
	kc := keychain.NewMock()
	_ = kc.Set("svc", "acc1", "a")
	_ = kc.Set("svc", "acc2", "b")
	v1, _ := kc.Get("svc", "acc1")
	v2, _ := kc.Get("svc", "acc2")
	if v1 != "a" || v2 != "b" {
		t.Errorf("account isolation broken: got %q, %q", v1, v2)
	}
}
```

- [ ] **Step 2: Run tests — they must fail (package doesn't exist yet)**

```bash
go test ./internal/keychain/...
```

Expected: `cannot find package` or `no such file`.

- [ ] **Step 3: Create `internal/keychain/keychain.go`**

```go
package keychain

import "errors"

// ErrNotFound is returned by Get and Delete when the account does not exist.
var ErrNotFound = errors.New("secret not found")

// Keychain abstracts OS secret store access.
// All methods accept a service and account identifier.
// Service is always "polimero"; account format is "<driver>:<profile>:<key>".
type Keychain interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}
```

- [ ] **Step 4: Create `internal/keychain/mock.go`**

```go
package keychain

import "sync"

// Mock is an in-memory Keychain implementation for use in tests.
type Mock struct {
	mu   sync.Mutex
	data map[string]string
}

// NewMock returns an empty in-memory keychain.
func NewMock() *Mock {
	return &Mock{data: make(map[string]string)}
}

func storeKey(service, account string) string { return service + "\x00" + account }

func (m *Mock) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[storeKey(service, account)]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (m *Mock) Set(service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[storeKey(service, account)] = secret
	return nil
}

func (m *Mock) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := storeKey(service, account)
	if _, ok := m.data[k]; !ok {
		return ErrNotFound
	}
	delete(m.data, k)
	return nil
}
```

- [ ] **Step 5: Create `internal/keychain/real.go`**

```go
package keychain

import (
	"errors"

	gokeyring "github.com/zalando/go-keyring"
)

// Real wraps go-keyring for OS-native secret storage.
type Real struct{}

// NewReal returns a Real keychain backed by the OS secret store.
func NewReal() *Real { return &Real{} }

func (r *Real) Get(service, account string) (string, error) {
	v, err := gokeyring.Get(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (r *Real) Set(service, account, secret string) error {
	return gokeyring.Set(service, account, secret)
}

func (r *Real) Delete(service, account string) error {
	err := gokeyring.Delete(service, account)
	if errors.Is(err, gokeyring.ErrNotFound) {
		return ErrNotFound
	}
	return err
}
```

- [ ] **Step 6: Run tests — they must pass**

```bash
go test ./internal/keychain/...
```

Expected: `ok github.com/polimero-app/cli/internal/keychain`

- [ ] **Step 7: Commit**

```bash
git add internal/keychain/
git commit -m "feat: add internal/keychain package with interface, mock, and real implementation"
```

---

## Task 3: `internal/tty` — Prompter interface, mock, real

**Files:**
- Create: `internal/tty/tty.go`
- Create: `internal/tty/mock.go`
- Create: `internal/tty/real.go`

(No separate unit tests — the Mock is exercised through command tests in Tasks 7 and 8.)

- [ ] **Step 1: Create `internal/tty/tty.go`**

```go
package tty

// Prompter abstracts terminal interaction for commands that need user input.
type Prompter interface {
	// IsTerminal reports whether stdin is an interactive terminal.
	IsTerminal() bool
	// ReadHidden prints prompt to stderr and reads a line with echo disabled.
	ReadHidden(prompt string) (string, error)
	// ReadLine prints prompt to stderr and reads a line with echo enabled.
	ReadLine(prompt string) (string, error)
}
```

- [ ] **Step 2: Create `internal/tty/mock.go`**

```go
package tty

// Mock is a Prompter implementation for use in tests.
// Set Terminal=true to simulate an interactive session.
// Set HiddenVal for ReadHidden responses; populate Lines for sequential ReadLine responses.
// Set Err to inject errors on all read calls.
type Mock struct {
	Terminal  bool
	HiddenVal string
	Lines     []string
	Err       error
	lineIdx   int
}

func (m *Mock) IsTerminal() bool { return m.Terminal }

func (m *Mock) ReadHidden(_ string) (string, error) {
	return m.HiddenVal, m.Err
}

func (m *Mock) ReadLine(_ string) (string, error) {
	if m.Err != nil {
		return "", m.Err
	}
	if m.lineIdx >= len(m.Lines) {
		return "", nil
	}
	v := m.Lines[m.lineIdx]
	m.lineIdx++
	return v, nil
}
```

- [ ] **Step 3: Create `internal/tty/real.go`**

```go
package tty

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// Real implements Prompter using the real terminal.
type Real struct{}

// NewReal returns a Real Prompter.
func NewReal() *Real { return &Real{} }

func (r *Real) IsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func (r *Real) ReadHidden(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	_, _ = fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *Real) ReadLine(prompt string) (string, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimRight(scanner.Text(), "\r\n"), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", nil
}
```

- [ ] **Step 4: Verify the package compiles**

```bash
go build ./internal/tty/...
```

Expected: no output (success).

- [ ] **Step 5: Commit**

```bash
git add internal/tty/
git commit -m "feat: add internal/tty package with Prompter interface, mock, and real implementation"
```

---

## Task 4: Config write operations

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_write_test.go`

The existing config.go already imports `errors`, `fmt`, `os`, `path/filepath`, `sort`, `time`, `gopkg.in/yaml.v3`. No new imports are needed for the additions below.

- [ ] **Step 1: Write the failing tests**

Create `internal/config/config_write_test.go`:

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

func makeProfile() config.Profile {
	now := time.Now().UTC().Truncate(time.Second)
	return config.Profile{
		Driver:  "bambu-lan",
		Host:    "192.0.2.10",
		Serial:  "01S09C450100XXX",
		Timeout: "10s",
		Created: now,
		Updated: now,
	}
}

func TestConfigDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("POLIMERO_CONFIG_DIR", dir)
	got, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("ConfigDir() = %q, want %q", got, dir)
	}
}

func TestGetProfile_Found(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("alpha", makeProfile())
	p, ok := cfg.GetProfile("alpha")
	if !ok {
		t.Fatal("expected profile to be found")
	}
	if p.Driver != "bambu-lan" {
		t.Errorf("Driver = %q, want bambu-lan", p.Driver)
	}
}

func TestGetProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	if _, ok := cfg.GetProfile("missing"); ok {
		t.Fatal("expected false for missing profile")
	}
}

func TestAddProfile_Duplicate(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("dup", makeProfile())
	if err := cfg.AddProfile("dup", makeProfile()); !errors.Is(err, config.ErrProfileAlreadyExists) {
		t.Errorf("expected ErrProfileAlreadyExists, got %v", err)
	}
}

func TestRemoveProfile_Found(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("toremove", makeProfile())

	removed, err := cfg.RemoveProfile("toremove")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Host != "192.0.2.10" {
		t.Errorf("removed.Host = %q, want 192.0.2.10", removed.Host)
	}
	if _, ok := cfg.GetProfile("toremove"); ok {
		t.Error("profile still present after remove")
	}
}

func TestRemoveProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	if _, err := cfg.RemoveProfile("missing"); !errors.Is(err, config.ErrProfileNotFound) {
		t.Errorf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	p := makeProfile()
	_ = cfg.AddProfile("myprinter", p)

	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	reloaded, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.GetProfile("myprinter")
	if !ok {
		t.Fatal("profile missing after reload")
	}
	if got.Host != p.Host {
		t.Errorf("Host = %q, want %q", got.Host, p.Host)
	}
	if got.Serial != p.Serial {
		t.Errorf("Serial = %q, want %q", got.Serial, p.Serial)
	}
}

func TestSave_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "polimero.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %04o, want 0600", perm)
	}
}

func TestSave_CreatesDirIfAbsent(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "new-subdir")
	cfg, _ := config.Open(dir)
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "polimero.yaml")); err != nil {
		t.Errorf("config file not created: %v", err)
	}
}

func TestSave_RemovePreservesOthers(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := config.Open(dir)
	_ = cfg.AddProfile("keep", makeProfile())
	_ = cfg.AddProfile("drop", makeProfile())
	_, _ = cfg.RemoveProfile("drop")
	_ = config.Save(dir, cfg)

	reloaded, _ := config.Open(dir)
	if _, ok := reloaded.GetProfile("keep"); !ok {
		t.Error("kept profile missing after save")
	}
	if _, ok := reloaded.GetProfile("drop"); ok {
		t.Error("dropped profile still present after save")
	}
}
```

- [ ] **Step 2: Run tests — they must fail**

```bash
go test ./internal/config/...
```

Expected: compilation failure — new symbols (`ConfigDir`, `AddProfile`, etc.) not defined yet.

- [ ] **Step 3: Add new symbols to `internal/config/config.go`**

Append the following block to the bottom of `internal/config/config.go` (after the existing `SortedProfiles` function):

```go
var (
	// ErrProfileAlreadyExists is returned by AddProfile when the name is taken.
	ErrProfileAlreadyExists = errors.New("profile already exists")
	// ErrProfileNotFound is returned by RemoveProfile when the name is absent.
	ErrProfileNotFound = errors.New("profile not found")
)

// ConfigDir resolves the config directory.
// POLIMERO_CONFIG_DIR env var overrides os.UserConfigDir()/polimero.
func ConfigDir() (string, error) {
	if d := os.Getenv("POLIMERO_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating config directory: %w", err)
	}
	return filepath.Join(base, "polimero"), nil
}

// GetProfile returns the named profile and true if found.
func (c *Config) GetProfile(name string) (Profile, bool) {
	p, ok := c.profiles[name]
	return p, ok
}

// AddProfile inserts a new profile. Returns ErrProfileAlreadyExists if the name is taken.
func (c *Config) AddProfile(name string, p Profile) error {
	if _, exists := c.profiles[name]; exists {
		return ErrProfileAlreadyExists
	}
	c.profiles[name] = p
	return nil
}

// RemoveProfile deletes the named profile and returns it.
// Returns ErrProfileNotFound if the name is absent.
func (c *Config) RemoveProfile(name string) (Profile, error) {
	p, ok := c.profiles[name]
	if !ok {
		return Profile{}, ErrProfileNotFound
	}
	delete(c.profiles, name)
	return p, nil
}

// Save atomically writes the config to dir/polimero.yaml with 0600 permissions.
// Creates dir if it does not exist (0700). Uses write-to-temp + rename for atomicity.
func Save(dir string, c *Config) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(configFile{Version: currentVersion, Profiles: c.profiles})
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".polimero-*.yaml")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeds

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("setting temp file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	return os.Rename(tmpName, filepath.Join(dir, "polimero.yaml"))
}
```

Also refactor `Load()` so it delegates to `ConfigDir()`:

Replace the existing `Load` function:

```go
// Load loads config from the OS default config dir.
// POLIMERO_CONFIG_DIR env var overrides the default (used in tests).
func Load() (*Config, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	return Open(dir)
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test ./internal/config/...
```

Expected: `ok github.com/polimero-app/cli/internal/config`

- [ ] **Step 5: Run full test suite to confirm no regressions**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 6: Commit**

```bash
git add internal/config/
git commit -m "feat: add config write operations (ConfigDir, AddProfile, RemoveProfile, Save)"
```

---

## Task 5: Driver interface and registry

**Files:**
- Create: `internal/driver/driver.go`

Note: `internal/drivers/bambulan/bambulan.go` and `internal/drivers/registry.go` are created in Task 6 — the registry import of it will cause a compile error until Task 6 is done. Write the registry test in Task 6 after the bambulan package exists.

- [ ] **Step 1: Create `internal/driver/driver.go`**

```go
package driver

import (
	"context"
	"time"
)

// Driver defines the interface every printer driver must satisfy.
// Plan 2 exposes Name and ConnectCheck only; Status and Capabilities are added in Plan 3.
type Driver interface {
	// Name returns the driver identifier string (e.g. "bambu-lan").
	Name() string

	// ConnectCheck verifies that the printer is reachable and credentials are valid.
	// Returns the SHA-256 leaf certificate fingerprint as "sha256:<lowercase-hex>".
	// Returns ("", nil) immediately when insecure is true.
	ConnectCheck(
		ctx context.Context,
		host, serial, accessCode string,
		insecure bool,
		timeout time.Duration,
	) (fingerprint string, err error)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/driver/...
```

Expected: no output.

- [ ] **Step 3: Commit driver interface alone**

```bash
git add internal/driver/
git commit -m "feat: add internal/driver interface (Plan 2: Name + ConnectCheck)"
```

---

## Task 6: Bambu-LAN connectivity check

**Files:**
- Create: `internal/drivers/bambulan/bambulan.go`
- Create: `internal/drivers/bambulan/bambulan_test.go`
- Create: `internal/drivers/registry.go`
- Create: `internal/drivers/registry_test.go`

- [ ] **Step 1: Write failing tests for the fast path and error classification**

Create `internal/drivers/bambulan/bambulan_test.go`:

```go
package bambulan_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/drivers/bambulan"
)

func TestName(t *testing.T) {
	if got := bambulan.New().Name(); got != "bambu-lan" {
		t.Errorf("Name() = %q, want %q", got, "bambu-lan")
	}
}

func TestConnectCheck_Insecure_NoConnection(t *testing.T) {
	drv := bambulan.New()
	fp, err := drv.ConnectCheck(context.Background(), "127.0.0.1", "SN123", "code", true, 5*time.Second)
	if err != nil {
		t.Fatalf("insecure ConnectCheck returned error: %v", err)
	}
	if fp != "" {
		t.Errorf("expected empty fingerprint for insecure mode, got %q", fp)
	}
}

func TestConnectCheck_UnreachableHost_ExitsCode4(t *testing.T) {
	// 192.0.2.1 is TEST-NET-1 (RFC 5737), guaranteed unreachable on any network.
	drv := bambulan.New()
	_, err := drv.ConnectCheck(context.Background(), "192.0.2.1", "SN123", "code", false, 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error connecting to unreachable host")
	}
	var exitErr *apperr.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if exitErr.Code != 4 {
		t.Errorf("exit code = %d, want 4 (network error)", exitErr.Code)
	}
}
```

- [ ] **Step 2: Run tests — they must fail**

```bash
go test ./internal/drivers/bambulan/...
```

Expected: `cannot find package`.

- [ ] **Step 3: Create `internal/drivers/bambulan/bambulan.go`**

```go
package bambulan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/polimero-app/cli/internal/apperr"
)

// Driver implements the bambu-lan protocol for Bambu Lab printers.
type Driver struct{}

// New returns a bambu-lan Driver.
func New() *Driver { return &Driver{} }

func (d *Driver) Name() string { return "bambu-lan" }

// ConnectCheck performs a full TLS+MQTT handshake to verify credentials.
// The leaf certificate SHA-256 fingerprint is captured via VerifyConnection
// (fired during paho's internal TLS dial — no second connection needed).
// Returns ("", nil) immediately when insecure=true.
//
// Exit codes on error:
//   - 3: MQTT auth rejected (CONNACK non-zero for bad credentials)
//   - 4: TLS dial failure, network timeout, or context cancelled
func (d *Driver) ConnectCheck(ctx context.Context, host, serial, accessCode string, insecure bool, timeout time.Duration) (string, error) {
	if insecure {
		return "", nil
	}

	var (
		mu      sync.Mutex
		leafDER []byte
	)

	tlsCfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // Bambu CA not in OS trust stores; leaf cert pinned by TOFU (ADR 0007)
		ServerName:         serial,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				mu.Lock()
				leafDER = cs.PeerCertificates[0].Raw
				mu.Unlock()
			}
			return nil
		},
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", host))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(accessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := mqtt.NewClient(opts)
	done := make(chan error, 1)
	go func() {
		token := client.Connect()
		token.Wait()
		done <- token.Error()
	}()

	select {
	case err := <-done:
		if err != nil {
			return "", classifyMQTTError(err)
		}
	case <-ctx.Done():
		return "", apperr.New(4, "connection cancelled")
	}
	client.Disconnect(250)

	mu.Lock()
	raw := make([]byte, len(leafDER))
	copy(raw, leafDER)
	mu.Unlock()

	if len(raw) == 0 {
		return "", apperr.New(4, "TLS handshake completed but no certificate received")
	}

	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// classifyMQTTError maps paho connect errors to apperr exit codes.
// CONNACK codes 4 (bad credentials) and 5 (not authorised) → exit 3.
// All other errors (network, TLS, timeout) → exit 4.
func classifyMQTTError(err error) error {
	if errors.Is(err, mqtt.ErrRefusedBadUsernameOrPassword) ||
		errors.Is(err, mqtt.ErrRefusedNotAuthorised) {
		return apperr.Newf(3, "MQTT authentication rejected: %s", err)
	}
	return apperr.Newf(4, "connection failed: %s", err)
}

func randomClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "polimero-" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test ./internal/drivers/bambulan/...
```

Expected: `ok github.com/polimero-app/cli/internal/drivers/bambulan`

- [ ] **Step 5: Write registry tests**

Create `internal/drivers/registry_test.go`:

```go
package drivers_test

import (
	"testing"

	"github.com/polimero-app/cli/internal/drivers"
)

func TestGet_KnownDriver(t *testing.T) {
	d, ok := drivers.Get("bambu-lan")
	if !ok {
		t.Fatal("expected bambu-lan in registry")
	}
	if d.Name() != "bambu-lan" {
		t.Errorf("d.Name() = %q, want bambu-lan", d.Name())
	}
}

func TestGet_UnknownDriver(t *testing.T) {
	if _, ok := drivers.Get("nonexistent"); ok {
		t.Fatal("expected false for unknown driver")
	}
}

func TestNames_ContainsBambuLan(t *testing.T) {
	for _, n := range drivers.Names() {
		if n == "bambu-lan" {
			return
		}
	}
	t.Error("bambu-lan missing from Names()")
}
```

- [ ] **Step 6: Run registry tests — they must fail**

```bash
go test ./internal/drivers/...
```

Expected: `cannot find package` for the registry.

- [ ] **Step 7: Create `internal/drivers/registry.go`**

```go
package drivers

import (
	"sort"

	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers/bambulan"
)

var registry = map[string]driver.Driver{
	"bambu-lan": bambulan.New(),
}

// Get returns the driver registered under name and true, or (nil, false) if not found.
func Get(name string) (driver.Driver, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names returns all registered driver names in alphabetical order.
func Names() []string {
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 8: Run all driver tests — they must pass**

```bash
go test ./internal/drivers/... ./internal/driver/...
```

Expected: both packages pass.

- [ ] **Step 9: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 10: Commit**

```bash
git add internal/drivers/ internal/driver/
git commit -m "feat: add driver interface, bambu-lan connectivity check, and driver registry"
```

---

## Task 7: `printer add` command

**Files:**
- Create: `cmd/printer/add.go`
- Create: `cmd/printer/add_test.go`
- Modify: `cmd/printer/printer.go`

The command takes `<name>` as a positional argument (`cobra.ExactArgs(1)`), not a flag.
The `AddDeps` type is exported so tests can inject mocks.

**Key spec rules:**
- Profile name: max 64 chars, ASCII letters/digits/`.`/`_`/`-`, must start with letter or digit, normalized to lowercase.
- Serial (bambu-lan): non-empty, printable ASCII (0x21–0x7E), max 64 chars.
- `--access-code-file`: regular file, ≤ 4 KiB, POSIX group/other bits must be 0, trim one trailing `\n` or `\r\n` only.
- Non-interactive + no file → exit 2.
- TTY prompt text: `Enter Bambu LAN access code for <name>: `
- Rollback: if fingerprint-keychain write fails → delete access-code entry; if config save fails → delete both entries.
- JSON data key: `"profile"` (not flat).
- Human output: multi-line (see spec Output section).

- [ ] **Step 1: Write failing tests**

Create `cmd/printer/add_test.go`:

```go
package printer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	inner   *keychain.Mock
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
```

- [ ] **Step 2: Run tests — they must fail**

```bash
go test ./cmd/printer/...
```

Expected: compilation failure — `printer.AddDeps` and `printer.AddCommandWithDeps` not defined yet.

- [ ] **Step 3: Create `cmd/printer/add.go`**

```go
package printer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

var profileNameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// AddDeps holds injectable dependencies for the printer add command.
// Tests supply mocks; the real command wires real implementations.
type AddDeps struct {
	KC        keychain.Keychain
	Prompter  tty.Prompter
	GetDriver func(name string) (driver.Driver, bool)
}

func addCommand() *cobra.Command {
	return AddCommandWithDeps(AddDeps{
		KC:        keychain.NewReal(),
		Prompter:  tty.NewReal(),
		GetDriver: drivers.Get,
	})
}

// AddCommandWithDeps constructs the "add" cobra command with injected dependencies.
func AddCommandWithDeps(deps AddDeps) *cobra.Command {
	var flags struct {
		driverName     string
		host           string
		serial         string
		timeout        string
		insecure       bool
		accessCodeFile string
	}

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a printer profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd, args[0], flags.driverName, flags.host, flags.serial,
				flags.timeout, flags.insecure, flags.accessCodeFile, deps)
		},
	}

	cmd.Flags().StringVar(&flags.driverName, "driver", "", "driver name (e.g. bambu-lan)")
	cmd.Flags().StringVar(&flags.host, "host", "", "printer IP or hostname")
	cmd.Flags().StringVar(&flags.serial, "serial", "", "printer serial number (required for bambu-lan)")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "10s", "connection timeout")
	cmd.Flags().BoolVar(&flags.insecure, "insecure", false, "skip TLS verification and MQTT auth check")
	cmd.Flags().StringVar(&flags.accessCodeFile, "access-code-file", "", "file containing the access code")
	_ = cmd.MarkFlagRequired("driver")
	_ = cmd.MarkFlagRequired("host")

	return cmd
}

func runAdd(cmd *cobra.Command, nameArg, driverName, host, serial, timeoutStr string, insecure bool, accessCodeFile string, deps AddDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	err := doAdd(cmd, nameArg, driverName, host, serial, timeoutStr, insecure, accessCodeFile, format, deps)
	if err == nil {
		return nil
	}
	return writeAddError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
}

func doAdd(cmd *cobra.Command, nameArg, driverName, host, serial, timeoutStr string, insecure bool, accessCodeFile string, format output.Format, deps AddDeps) error {
	name := strings.ToLower(nameArg)

	// 1. Validate
	if err := validateProfileName(name); err != nil {
		return err
	}
	drv, ok := deps.GetDriver(driverName)
	if !ok {
		return apperr.Newf(2, "unknown driver %q; valid drivers: %s", driverName, strings.Join(drivers.Names(), ", "))
	}
	if driverName == "bambu-lan" {
		if err := validateSerial(serial); err != nil {
			return err
		}
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		return apperr.Newf(2, "invalid --timeout %q: %s", timeoutStr, err)
	}

	dir, err := config.ConfigDir()
	if err != nil {
		return apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return apperr.Newf(2, "cannot load config: %s", err)
	}
	if _, exists := cfg.GetProfile(name); exists {
		return apperr.Newf(2, "printer profile %q already exists", name)
	}

	// 2. Collect access code
	var accessCode string
	if accessCodeFile != "" {
		accessCode, err = readAccessCodeFile(accessCodeFile)
		if err != nil {
			return err
		}
	} else if deps.Prompter.IsTerminal() {
		accessCode, err = deps.Prompter.ReadHidden(fmt.Sprintf("Enter Bambu LAN access code for %s: ", name))
		if err != nil {
			return apperr.Newf(1, "cannot read access code: %s", err)
		}
	} else {
		return apperr.New(2, "non-interactive mode requires --access-code-file")
	}

	kcAcct := fmt.Sprintf("%s:%s:access-code", driverName, name)
	kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", driverName, name)

	var fingerprint string
	if !insecure {
		// 3. Connectivity check (TLS + MQTT CONNECT + CONNACK)
		ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
		defer cancel()
		fingerprint, err = drv.ConnectCheck(ctx, host, serial, accessCode, false, timeout)
		if err != nil {
			return err // already an *apperr.ExitError with code 3 or 4
		}

		// 4. Store access code
		if err := deps.KC.Set("polimero", kcAcct, accessCode); err != nil {
			return apperr.Newf(3, "cannot store access code in keychain: %s", err)
		}

		// 5. Store TLS fingerprint; rollback access code on failure
		if err := deps.KC.Set("polimero", kcFpAcct, fingerprint); err != nil {
			_ = deps.KC.Delete("polimero", kcAcct)
			return apperr.Newf(3, "cannot store TLS fingerprint in keychain: %s", err)
		}
	} else {
		// Insecure: 3. Store access code (no connectivity check, no fingerprint)
		if err := deps.KC.Set("polimero", kcAcct, accessCode); err != nil {
			return apperr.Newf(3, "cannot store access code in keychain: %s", err)
		}
	}

	// 6. Write profile; rollback keychain entries on failure
	now := time.Now().UTC()
	p := config.Profile{
		Driver:   driverName,
		Host:     host,
		Serial:   serial,
		Timeout:  timeoutStr,
		Insecure: insecure,
		Created:  now,
		Updated:  now,
	}
	if err := cfg.AddProfile(name, p); err != nil {
		_ = deps.KC.Delete("polimero", kcAcct)
		if !insecure {
			_ = deps.KC.Delete("polimero", kcFpAcct)
		}
		return apperr.Newf(1, "cannot add profile: %s", err)
	}
	if err := config.Save(dir, cfg); err != nil {
		_ = deps.KC.Delete("polimero", kcAcct)
		if !insecure {
			_ = deps.KC.Delete("polimero", kcFpAcct)
		}
		return apperr.Newf(1, "cannot save config: %s", err)
	}

	// 7. Output success
	return writeAddSuccess(cmd.OutOrStdout(), format, name, p, fingerprint)
}

func writeAddSuccess(w io.Writer, format output.Format, name string, p config.Profile, fingerprint string) error {
	if format == output.FormatJSON {
		var fp any
		if fingerprint != "" {
			fp = fingerprint
		}
		return output.WriteEnvelope(w, output.Envelope{
			OK: true,
			Data: map[string]any{
				"profile": map[string]any{
					"name":           name,
					"driver":         p.Driver,
					"host":           p.Host,
					"serial":         p.Serial,
					"timeout":        p.Timeout,
					"insecure":       p.Insecure,
					"tlsFingerprint": fp,
				},
			},
			Error: nil,
			Meta:  output.Meta{Command: "printer add"},
		})
	}
	lines := []string{
		fmt.Sprintf("Printer profile added: %s", name),
		fmt.Sprintf("Driver: %s", p.Driver),
		fmt.Sprintf("Host: %s", p.Host),
		fmt.Sprintf("Serial: %s", p.Serial),
	}
	if p.Insecure {
		lines = append(lines, "Warning: TLS verification is disabled for this profile.")
	} else {
		lines = append(lines, fmt.Sprintf("TLS: %s", fingerprint))
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, l); err != nil {
			return err
		}
	}
	return nil
}

func writeAddError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: addErrorCode(err), Message: err.Error()},
			Meta:  output.Meta{Command: "printer add"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
	}
	return apperr.New(code, "")
}

func addErrorCode(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		return "profile_exists"
	case strings.Contains(msg, "unknown driver"):
		return "unknown_driver"
	case strings.Contains(msg, "MQTT authentication"):
		return "auth_error"
	case strings.Contains(msg, "connection failed"), strings.Contains(msg, "connection cancelled"):
		return "network_error"
	case strings.Contains(msg, "keychain"):
		return "keychain_error"
	default:
		return "error"
	}
}

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

func validateSerial(serial string) error {
	if serial == "" {
		return apperr.New(2, "--serial is required for bambu-lan driver")
	}
	if len(serial) > 64 {
		return apperr.Newf(2, "--serial too long (max 64 chars)")
	}
	for _, c := range serial {
		if c < 0x21 || c > 0x7E {
			return apperr.Newf(2, "--serial contains invalid character (must be printable ASCII with no whitespace)")
		}
	}
	return nil
}

func readAccessCodeFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", apperr.Newf(2, "--access-code-file: %s", err)
	}
	if !info.Mode().IsRegular() {
		return "", apperr.Newf(2, "--access-code-file %q is not a regular file", path)
	}
	if info.Size() > 4096 {
		return "", apperr.Newf(2, "--access-code-file %q exceeds 4 KiB limit", path)
	}
	if info.Mode().Perm()&0077 != 0 {
		return "", apperr.Newf(2, "--access-code-file %q has insecure permissions: group or other access detected", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", apperr.Newf(2, "--access-code-file: %s", err)
	}
	return trimTrailingNewline(string(data)), nil
}

// trimTrailingNewline removes exactly one trailing \r\n or \n.
// Other leading or trailing whitespace is preserved per spec.
func trimTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\r\n") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "\n") {
		return s[:len(s)-1]
	}
	return s
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test ./cmd/printer/...
```

Expected: all tests in the printer package pass.

- [ ] **Step 5: Wire the add command into `cmd/printer/printer.go`**

Replace the existing `printer.go` content:

```go
package printer

import (
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(listCommand())
	cmd.AddCommand(AddCommandWithDeps(AddDeps{
		KC:        keychain.NewReal(),
		Prompter:  tty.NewReal(),
		GetDriver: drivers.Get,
	}))
	return cmd
}
```

- [ ] **Step 6: Run full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 7: Commit**

```bash
git add cmd/printer/add.go cmd/printer/add_test.go cmd/printer/printer.go
git commit -m "feat: implement printer add command with bambu-lan TLS+MQTT connectivity check"
```

---

## Task 8: `printer remove` command

**Files:**
- Create: `cmd/printer/remove.go`
- Create: `cmd/printer/remove_test.go`
- Modify: `cmd/printer/printer.go`

**Key spec rules:**
- `<name>` is a positional argument, normalized to lowercase.
- Interactive confirmation required unless `--yes` is given. Prompt: `Remove printer profile <name> and its stored secrets? Type 'yes' to continue: `
- Non-interactive + no `--yes` → exit 2.
- Keychain deletions are independent; `ErrNotFound` produces a per-entry warning (not a fatal error).
- `Insecure=true` profile: missing TLS fingerprint is NOT a warning (expected absence).
- `Insecure=false` profile: missing TLS fingerprint IS a warning (`tls_fingerprint_not_found`).
- JSON response: `data.removed.{name, accessCodeRemoved, tlsFingerprintRemoved}` + `data.warnings` (always an array, never null).

- [ ] **Step 1: Write failing tests**

Create `cmd/printer/remove_test.go`:

```go
package printer_test

import (
	"bytes"
	"encoding/json"
	"errors"
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
```

- [ ] **Step 2: Run tests — they must fail**

```bash
go test ./cmd/printer/...
```

Expected: compilation failure — `printer.RemoveDeps` and `printer.RemoveCommandWithDeps` not defined.

- [ ] **Step 3: Create `cmd/printer/remove.go`**

```go
package printer

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/config"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/output"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// RemoveDeps holds injectable dependencies for the printer remove command.
type RemoveDeps struct {
	KC       keychain.Keychain
	Prompter tty.Prompter
}

func removeCommand() *cobra.Command {
	return RemoveCommandWithDeps(RemoveDeps{
		KC:       keychain.NewReal(),
		Prompter: tty.NewReal(),
	})
}

// RemoveCommandWithDeps constructs the "remove" cobra command with injected dependencies.
func RemoveCommandWithDeps(deps RemoveDeps) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a printer profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemove(cmd, args[0], yes, deps)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip interactive confirmation")
	return cmd
}

type removeWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runRemove(cmd *cobra.Command, nameArg string, yes bool, deps RemoveDeps) error {
	formatStr, _ := cmd.Root().PersistentFlags().GetString("output")
	format, fmtErr := output.ParseFormat(formatStr)
	if fmtErr != nil {
		return apperr.New(2, fmtErr.Error())
	}

	err := doRemove(cmd, nameArg, yes, format, deps)
	if err == nil {
		return nil
	}
	return writeRemoveError(cmd.OutOrStdout(), cmd.ErrOrStderr(), format, err)
}

func doRemove(cmd *cobra.Command, nameArg string, yes bool, format output.Format, deps RemoveDeps) error {
	name := strings.ToLower(nameArg)

	dir, err := config.ConfigDir()
	if err != nil {
		return apperr.Newf(1, "cannot resolve config directory: %s", err)
	}
	cfg, err := config.Open(dir)
	if err != nil {
		return apperr.Newf(2, "cannot load config: %s", err)
	}
	p, ok := cfg.GetProfile(name)
	if !ok {
		return apperr.Newf(2, "printer profile %q not found", name)
	}

	// Confirmation
	if !yes {
		if !deps.Prompter.IsTerminal() {
			return apperr.New(2, "non-interactive mode requires --yes")
		}
		answer, err := deps.Prompter.ReadLine(
			fmt.Sprintf("Remove printer profile %s and its stored secrets? Type 'yes' to continue: ", name),
		)
		if err != nil {
			return apperr.Newf(1, "cannot read confirmation: %s", err)
		}
		if answer != "yes" {
			return apperr.New(2, "confirmation declined; profile not removed")
		}
	}

	var warnings []removeWarning
	accessCodeRemoved := false
	tlsFingerprintRemoved := false

	// Delete access code (missing = warning, not fatal)
	kcAcct := fmt.Sprintf("%s:%s:access-code", p.Driver, name)
	if err := deps.KC.Delete("polimero", kcAcct); err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			warnings = append(warnings, removeWarning{
				Code:    "access_code_not_found",
				Message: "profile was removed, but no stored access code was found",
			})
		} else {
			return apperr.Newf(3, "cannot delete access code from keychain: %s", err)
		}
	} else {
		accessCodeRemoved = true
	}

	// Delete TLS fingerprint (missing on insecure profile = expected, no warning)
	kcFpAcct := fmt.Sprintf("%s:%s:tls-fingerprint", p.Driver, name)
	if err := deps.KC.Delete("polimero", kcFpAcct); err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			if !p.Insecure {
				warnings = append(warnings, removeWarning{
					Code:    "tls_fingerprint_not_found",
					Message: "profile was removed, but no stored TLS fingerprint was found",
				})
			}
		} else {
			return apperr.Newf(3, "cannot delete TLS fingerprint from keychain: %s", err)
		}
	} else {
		tlsFingerprintRemoved = true
	}

	if _, err := cfg.RemoveProfile(name); err != nil {
		return apperr.Newf(1, "cannot remove profile: %s", err)
	}
	if err := config.Save(dir, cfg); err != nil {
		return apperr.Newf(1, "cannot save config; keychain entries may have already been deleted: %s", err)
	}

	return writeRemoveSuccess(cmd.OutOrStdout(), format, name, accessCodeRemoved, tlsFingerprintRemoved, warnings)
}

func writeRemoveSuccess(w io.Writer, format output.Format, name string, accessCodeRemoved, tlsFingerprintRemoved bool, warnings []removeWarning) error {
	if format == output.FormatJSON {
		warningsOut := make([]any, len(warnings))
		for i, ww := range warnings {
			warningsOut[i] = map[string]any{"code": ww.Code, "message": ww.Message}
		}
		return output.WriteEnvelope(w, output.Envelope{
			OK: true,
			Data: map[string]any{
				"removed": map[string]any{
					"name":                  name,
					"accessCodeRemoved":     accessCodeRemoved,
					"tlsFingerprintRemoved": tlsFingerprintRemoved,
				},
				"warnings": warningsOut,
			},
			Error: nil,
			Meta:  output.Meta{Command: "printer remove"},
		})
	}
	_, err := fmt.Fprintf(w, "Printer profile removed: %s\n", name)
	return err
}

func writeRemoveError(out, errOut io.Writer, format output.Format, err error) error {
	var exitErr *apperr.ExitError
	code := 1
	if errors.As(err, &exitErr) {
		code = exitErr.Code
	}
	if format == output.FormatJSON {
		_ = output.WriteEnvelope(out, output.Envelope{
			OK:    false,
			Data:  nil,
			Error: &output.ErrDetail{Code: "error", Message: err.Error()},
			Meta:  output.Meta{Command: "printer remove"},
		})
	} else {
		_, _ = fmt.Fprintf(errOut, "Error: %s\n", err)
	}
	return apperr.New(code, "")
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test ./cmd/printer/...
```

Expected: all tests pass.

- [ ] **Step 5: Wire the remove command into `cmd/printer/printer.go`**

Replace the full content of `cmd/printer/printer.go`:

```go
package printer

import (
	"github.com/polimero-app/cli/internal/drivers"
	"github.com/polimero-app/cli/internal/keychain"
	"github.com/polimero-app/cli/internal/tty"
	"github.com/spf13/cobra"
)

// Command returns the "printer" subcommand with all its children attached.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "printer",
		Short: "Manage 3D printer profiles",
	}
	cmd.AddCommand(listCommand())
	cmd.AddCommand(AddCommandWithDeps(AddDeps{
		KC:        keychain.NewReal(),
		Prompter:  tty.NewReal(),
		GetDriver: drivers.Get,
	}))
	cmd.AddCommand(RemoveCommandWithDeps(RemoveDeps{
		KC:       keychain.NewReal(),
		Prompter: tty.NewReal(),
	}))
	return cmd
}
```

- [ ] **Step 6: Run the full test suite and linter**

```bash
go test ./...
make lint
```

Expected: all tests pass; linter clean.

- [ ] **Step 7: Commit**

```bash
git add cmd/printer/remove.go cmd/printer/remove_test.go cmd/printer/printer.go
git commit -m "feat: implement printer remove command with keychain cleanup and confirmation"
```

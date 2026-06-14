# Plan 2 Design: `printer add` and `printer remove`

**Date:** 2026-06-13
**Scope:** Milestone 2 — printer add, printer remove, bambu-lan connectivity check, keychain and TTY abstractions, config write operations, minimal driver interface.

---

## 1. Package Structure and New Dependencies

### New packages

| Package | Purpose |
|---|---|
| `internal/keychain` | Keychain interface + OS implementation (go-keyring) + mock |
| `internal/tty` | Hidden prompt interface + real terminal implementation |
| `internal/driver` | Minimal `Driver` interface for Plan 2 surface only |
| `internal/drivers/bambulan` | Bambu LAN connectivity check (TLS + MQTT) |
| `internal/drivers` | Driver registry (simple map, `registry.go`) |
| `cmd/printer/add.go` | `printer add` command |
| `cmd/printer/remove.go` | `printer remove` command |

Config write methods are added to the existing `internal/config` package (no new package).

### New third-party dependencies

| Module | Use |
|---|---|
| `github.com/zalando/go-keyring` | macOS Keychain, Linux Secret Service, Windows Credential Manager |
| `github.com/eclipse/paho.mqtt.golang` | MQTT client for TLS connect + CONNACK (reused in Plan 3 for status) |
| `golang.org/x/term` | `term.ReadPassword()` for hidden access code input |

---

## 2. Config Write Operations

Three new methods on `*Config` and one package-level function:

```go
// GetProfile returns the named profile. Name must already be lowercased by caller.
func (c *Config) GetProfile(name string) (Profile, bool)

// AddProfile adds a new profile. Returns error if name already exists.
func (c *Config) AddProfile(name string, p Profile) error

// RemoveProfile removes the named profile. Returns the removed profile,
// or error if not found.
func (c *Config) RemoveProfile(name string) (Profile, error)

// Save atomically writes the config to dir/polimero.yaml with 0600 permissions.
// Creates the directory if absent (0700). Uses write-to-temp + rename for atomicity.
func Save(dir string, c *Config) error

// ConfigDir resolves the config directory: POLIMERO_CONFIG_DIR env var if set,
// otherwise os.UserConfigDir()/polimero.
func ConfigDir() (string, error)
```

Key decisions:
- **Atomic write**: temp file in the same directory → `os.Rename`. Same-directory temp avoids cross-device rename.
- **Permissions**: file `0600`, directory `0700` — owner-only as required by the secret-handling spec.
- **Name normalization**: callers (commands) lowercase the name before calling `AddProfile`/`RemoveProfile`. The config package stores what it receives without further transformation.
- **`ConfigDir()` helper**: resolves the `POLIMERO_CONFIG_DIR` env var once; the command layer calls it once and passes the result to both `Open` and `Save`. Avoids repeating the resolution logic.

---

## 3. Keychain and TTY Abstractions

### Keychain

```go
// internal/keychain/keychain.go

type Keychain interface {
    Get(service, account string) (string, error)
    Set(service, account, secret string) error
    Delete(service, account string) error
}

var ErrNotFound = errors.New("secret not found")
```

- `ErrNotFound` is returned by `Get` and `Delete` when the account does not exist. The real implementation maps `go-keyring`'s not-found error to this sentinel.
- Service name is always `"polimero"` — hardcoded at the call site in commands, not inside the keychain package (keeps the package generic).
- Account name format: `"<driver>:<name>:access-code"` and `"<driver>:<name>:tls-fingerprint"` — constructed by the command layer.

Files:
- `internal/keychain/keychain.go` — interface + `ErrNotFound`
- `internal/keychain/real.go` — wraps `go-keyring`
- `internal/keychain/mock.go` — in-memory map; used by all tests

### TTY

```go
// internal/tty/tty.go

type Prompter interface {
    ReadHidden(prompt string) (string, error)
}
```

- Real implementation: prints `prompt` to stderr, calls `term.ReadPassword(int(os.Stdin.Fd()))`, prints a newline after.
- Tests inject a mock `Prompter` that returns a preset value.
- No TTY detection: if `--access-code-file` is provided, the command takes that path and never calls `ReadHidden`. The choice is made before any call to the Prompter.

---

## 4. Driver Interface and Bambu-LAN Connectivity Check

### `internal/driver/driver.go`

```go
type Driver interface {
    // Name returns the driver identifier (e.g. "bambu-lan").
    Name() string

    // ConnectCheck performs a full TLS+MQTT handshake to verify host
    // reachability and credential validity. Returns the SHA-256 leaf cert
    // fingerprint as "sha256:<lowercase-hex>".
    // Returns ("", nil) when insecure=true (no connection made).
    ConnectCheck(
        ctx context.Context,
        host, serial, accessCode string,
        insecure bool,
        timeout time.Duration,
    ) (fingerprint string, err error)
}
```

Plan 3 will extend this interface with `Capabilities()` and `Status()`. No stubs or placeholders here.

### `internal/drivers/registry.go`

```go
var drivers = map[string]driver.Driver{
    "bambu-lan": bambulan.New(),
}

func Get(name string) (driver.Driver, bool) {
    d, ok := drivers[name]
    return d, ok
}
```

### `internal/drivers/bambulan/bambulan.go` — ConnectCheck

Sequence when `insecure=false`:

1. Return `("", nil)` immediately when `insecure=true`.
2. Prepare a `tls.Config` with `InsecureSkipVerify: true`, `ServerName: serial`, and a `VerifyConnection` callback that captures `cs.PeerCertificates[0]` into a shared variable (runs inside the TLS handshake, before paho proceeds).
3. Pass the `tls.Config` to paho via `options.SetTLSConfig(...)`. Paho dials and performs the TLS handshake — the callback fires and records the leaf cert.
4. Call `client.Connect()`, wait for token, check `token.Error()`.
5. Call `client.Disconnect(250)`.
6. Compute `"sha256:" + hex.EncodeToString(sha256sum(cert.Raw))` from the captured cert.
7. Return fingerprint.

This single-dial design: paho opens the TLS connection; the `VerifyConnection` callback fires during that handshake. No second dial needed to capture the cert.

**Exit code mapping** (errors wrapped as `*apperr.ExitError`):
- TLS dial failure or timeout → exit code `4`
- MQTT CONNACK non-zero (auth rejected) → exit code `3`

---

## 5. Command Flows

### `printer add` — flag set

| Flag | Type | Required | Notes |
|---|---|---|---|
| `--name` | string | yes | normalized to lowercase |
| `--driver` | string | yes | must exist in registry |
| `--host` | string | yes | |
| `--serial` | string | required for bambu-lan | stored verbatim; used for SNI and MQTT topics |
| `--timeout` | string | no | default `"30s"` |
| `--insecure` | bool | no | skips connectivity check |
| `--access-code-file` | string | no | reads access code from file instead of TTY |

**Execution sequence:**

1. **Validate flags** — name non-empty, driver in registry, host non-empty; serial required when driver=bambu-lan. Exit 2 on failure.
2. **Normalize name** — `strings.ToLower`. Load config; check for duplicate name. Exit 2 if duplicate.
3. **Collect access code** — if `--access-code-file` given, read file and apply `strings.TrimSpace`; else call `prompter.ReadHidden("Access code: ")`.
4. **Driver connectivity check** — `drv.ConnectCheck(ctx, host, serial, accessCode, insecure, timeout)`. Return the driver's `*apperr.ExitError` on failure (exit 3 or 4 as appropriate).
5. **Store keychain entries** — `kc.Set("polimero", "<driver>:<name>:access-code", accessCode)`. If not insecure, also `kc.Set("polimero", "<driver>:<name>:tls-fingerprint", fingerprint)`. Exit 1 on keychain failure.
6. **Write profile** — `cfg.AddProfile(name, p)` → `config.Save(dir, cfg)`. Exit 1 on failure. If save fails, attempt best-effort cleanup of keychain entries (log warning if cleanup also fails).
7. **Output** — human: `Added printer "<name>".`; JSON: full profile including `serial` and `tlsFingerprint` (null when insecure).

**Dependency injection** — `addDeps` struct holds `keychain.Keychain` and `tty.Prompter`. Tests inject mocks; the real command wires real implementations.

### `printer remove` — flag set

| Flag | Type | Required |
|---|---|---|
| `--name` | string | yes |

**Execution sequence:**

1. **Load config** — resolve profile by lowercase name. Exit 2 if not found.
2. **Delete keychain entries** — attempt `access-code` delete, then `tls-fingerprint` delete, independently. `ErrNotFound` produces a per-entry warning (`access_code_not_found`, `tls_fingerprint_not_found`); other errors are fatal (exit 1).
3. **Remove profile** — `cfg.RemoveProfile(name)` → `config.Save(dir, cfg)`. Exit 1 on failure.
4. **Output** — human: `Removed printer "<name>".` (with warnings appended if any); JSON envelope with `warnings` array (empty array when none, never null).

Same `keychain.Keychain` injection pattern for tests.

---

## Testing Strategy

- `internal/config` write tests: temp dir, real YAML round-trips, atomic-write verification (file perms, content correctness after Save).
- `internal/keychain/mock.go`: in-memory map; all command-layer tests use this.
- `internal/tty`: mock Prompter returns preset string; no real terminal in tests.
- `internal/drivers/bambulan`: connectivity check tested against a local TLS+MQTT test server (or skipped with `//go:build integration`). Unit tests cover the `insecure=true` fast path and error wrapping.
- `cmd/printer/add_test.go` and `cmd/printer/remove_test.go`: inject mock keychain, prompter, and a stub driver. Cover golden path, duplicate name, connectivity failure, keychain failure, save failure, and rollback logging.
- All tests use `t.Setenv("POLIMERO_CONFIG_DIR", t.TempDir())` for config isolation.

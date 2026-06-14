# Plan 3 Design: `printer status`

**Date:** 2026-06-14
**Scope:** Milestone 3 — `printer status` command, driver interface extension (`Capabilities`, `Status`), Bambu LAN MQTT status flow, TOFU fingerprint verification, portable status type definitions.

---

## 1. New Types in `internal/driver`

Split into two files:

### `internal/driver/driver.go` — updated interface

```go
type Driver interface {
    Name() string
    Capabilities() Capabilities
    ConnectCheck(ctx context.Context, host, serial, accessCode string, insecure bool, timeout time.Duration) (string, error)
    Status(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (*StatusResult, error)
}
```

Adding `Capabilities()` and `Status()` to the existing interface breaks the build until `bambulan.Driver` implements both — intentional TDD pressure.

### `internal/driver/types.go` — all shared data types

```go
type Capabilities struct {
    Status           bool
    Discovery        bool
    JobUpload        bool
    JobStart         bool
    JobPause         bool
    JobCancel        bool
    TemperatureRead  bool
    TemperatureWrite bool
    MotionControl    bool
}

type SecretsBundle struct {
    AccessCode     string // printer LAN access code
    TLSFingerprint string // "sha256:<hex>"; empty when insecure
}

type ProfileInput struct {
    Name     string
    Driver   string
    Host     string
    Serial   string
    Timeout  time.Duration
    Insecure bool
}

type Temperature struct {
    CurrentCelsius float64  `json:"currentCelsius"`
    TargetCelsius  *float64 `json:"targetCelsius,omitempty"` // nil for chamber (no target)
}

type Temperatures struct {
    Nozzle  *Temperature `json:"nozzle,omitempty"`
    Bed     *Temperature `json:"bed,omitempty"`
    Chamber *Temperature `json:"chamber,omitempty"`
}

type Job struct {
    Name string `json:"name"`
}

type Progress struct {
    Percent      int  `json:"percent"`
    CurrentLayer *int `json:"currentLayer,omitempty"`
    TotalLayers  *int `json:"totalLayers,omitempty"`
}

type StatusError struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

type StatusWarning struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

type StatusResult struct {
    State        string          `json:"state"`
    Temperatures *Temperatures   `json:"temperatures"`
    Job          *Job            `json:"job"`
    Progress     *Progress       `json:"progress"`
    Errors       []StatusError   `json:"errors"`
    Warnings     []StatusWarning `json:"warnings"`
    Capabilities Capabilities    `json:"capabilities"`
}
```

`Temperatures` is `nil` (serializes to `null`) when none of the three temperature fields are present in the payload. Individual sensors (`Chamber`) are `nil` and omitted via `omitempty` when absent from the payload — not set to `null`.

`Errors` and `Warnings` must always be initialized as empty slices (never nil) so they serialize as `[]` rather than `null`. `parseReport` is responsible for this initialization.

---

## 2. MQTT Abstraction in `internal/drivers/bambulan`

### `mqttConn` interface (package-internal)

```go
type mqttConn interface {
    Connect() mqtt.Token
    Subscribe(topic string, qos byte, callback mqtt.MessageHandler) mqtt.Token
    Publish(topic string, qos byte, retained bool, payload any) mqtt.Token
    Disconnect(quiesce uint)
}
```

`mqtt.Client` (paho) already satisfies this — no production wrapper needed.

### Driver struct

```go
type Driver struct {
    newClient func(*mqtt.ClientOptions) mqttConn
}

func New() *Driver {
    return &Driver{
        newClient: func(o *mqtt.ClientOptions) mqttConn { return mqtt.NewClient(o) },
    }
}
```

Tests supply a `fakeClient` that satisfies `mqttConn`.

### Shared TLS config builder

```go
func buildTLSConfig(serial, fingerprint string, insecure bool) (*tls.Config, error)
```

When `insecure` is false, `VerifyConnection` computes the SHA-256 fingerprint of the presented leaf cert and compares it to `fingerprint`. A mismatch returns a `fingerprintMismatchError` — a package-private sentinel type — which the error classifier maps to exit code 3.

`ConnectCheck` is refactored to use `buildTLSConfig`. Its existing behaviour is unchanged.

---

## 3. `bambulan.Status` Implementation

```
Connect (TLS + MQTT auth)
  └─ buildTLSConfig: VerifyConnection checks fingerprint (→ exit 3 on mismatch)
Subscribe device/{serial}/report
  └─ handler sends raw []byte into buffered chan (size 1)
Publish pushall to device/{serial}/request
Wait: select { case msg := <-ch | case <-ctx.Done() }
  └─ timeout → apperr.New(4, "timeout")
Disconnect (250 ms quiesce)
Parse payload → *StatusResult
```

**Pushall payload:**
```json
{"pushing": {"sequence_id": "1", "command": "pushall", "version": 1, "push_target": 1}}
```

**Parsing** is a pure function `parseReport(data []byte) (*driver.StatusResult, error)`. It unmarshals only the `print` sub-object and calls `mapState`, `mapTemperatures`, `mapJob`, `mapProgress`, `mapErrors`. No network dependency — unit-testable with raw JSON byte slices.

**State mapping** (`mapState`):

| Bambu `gcode_state` | Portable state |
|---|---|
| `IDLE`, `FINISH` | `idle` |
| `PRINTING`, `PREPARE`, `RUNNING`, `SLICING` | `printing` |
| `PAUSED` | `paused` |
| `FAILED` | `error` |
| _(anything else)_ | `unknown` |

**Error classification** (`classifyStatusError`):

| Condition | Exit code |
|---|---|
| `fingerprintMismatchError` | 3 |
| CONNACK auth rejected | 3 |
| TLS/network failure | 4 |
| Context deadline exceeded | 4 |

**`Capabilities()`** returns `Capabilities{Status: true}` — all other fields false.

### `fakeClient` (test helper in `bambulan_test.go`)

- `Connect()` returns a pre-built passing or failing token (controlled by test).
- `Subscribe(topic, qos, handler)` stores the handler and immediately calls it with a canned `[]byte` payload (injected by the test).
- `Publish(...)` no-op.
- `Disconnect()` no-op.

A `fakeToken` struct satisfies `mqtt.Token`. A `fakeMessage` struct satisfies `mqtt.Message`.

---

## 4. `cmd/printer/status.go` Command

### Flags

| Flag | Type | Notes |
|---|---|---|
| `<name>` | positional | normalized to lowercase; show help if omitted |
| `--timeout` | string | overrides profile timeout |
| `--insecure` | bool | skips TLS verification for this invocation |

### `StatusDeps`

```go
type StatusDeps struct {
    KC        keychain.Keychain
    GetDriver func(string) (driver.Driver, bool)
    Log       *slog.Logger
}
```

### Execution sequence

1. Normalize name to lowercase. Load config; look up profile → exit 2 if not found.
2. Resolve timeout: `--timeout` flag overrides profile's stored string; parse with `time.ParseDuration`. Default `10s`.
3. Effective insecure: `p.Insecure || insecureFlag`.
4. Load secrets:
   - Access code → exit 3 on `ErrNotFound`.
   - TLS fingerprint → exit 3 on `ErrNotFound` when not insecure. Skipped when insecure.
5. Look up driver → exit 2 if unknown.
6. Check `drv.Capabilities().Status` → exit 5 if false.
7. `start := time.Now()`. Call `drv.Status(ctx, profileInput, secrets, deps.Log)`. Compute `durationMs`.
8. On error: classify and render error envelope (`durationMs` omitted per spec).
9. On success: render human or JSON output with `durationMs` in meta.

### Human output

Print only fields that are present. Warnings printed as a block at the end:

```
Printer: garage-x1c
State: printing
Progress: 42%
Nozzle: 215.0 °C / 220.0 °C
Bed: 60.0 °C / 60.0 °C
Job: bracket.3mf
```

### JSON output

Follows the envelope shape from the command spec, including `durationMs` in `meta`.

### Tests

| Scenario | Expected |
|---|---|
| Full status from stub driver | exit 0, all fields present |
| Partial status with warnings | exit 0, warnings in output |
| No args | help printed |
| Profile not found | exit 2 |
| Missing access code | exit 3 |
| Missing TLS fingerprint (secure profile) | exit 3 |
| `profile.insecure: true` | skips fingerprint load |
| `--insecure` flag | skips fingerprint load |
| Auth failure from driver | exit 3 |
| Timeout from driver | exit 4 |
| Unsupported capability | exit 5 |
| JSON envelope shape | `ok`, `data`, `meta.durationMs`, `meta.command = "printer status"` present |

---

## 5. File Map

| File | Action |
|---|---|
| `internal/driver/driver.go` | Modify — add `Capabilities()` and `Status()` to interface |
| `internal/driver/types.go` | Create — all shared types |
| `internal/drivers/bambulan/bambulan.go` | Modify — add `mqttConn`, `buildTLSConfig`, `fingerprintMismatchError`, `Capabilities()`, `Status()`, `parseReport` and helpers; refactor `ConnectCheck` |
| `internal/drivers/bambulan/bambulan_test.go` | Modify — add new tests; add `fakeClient`, `fakeToken`, `fakeMessage` |
| `cmd/printer/status.go` | Create — `printer status` command |
| `cmd/printer/status_test.go` | Create — command tests with stub driver |
| `cmd/printer/printer.go` | Modify — wire `statusCommand()` |

---

## 6. Testing Strategy

- `parseReport` tested with raw JSON byte slices — no network, no paho.
- `mapState` tested with all known `gcode_state` values plus unknown inputs.
- `bambulan.Status` tested via `fakeClient` injection: happy path, partial fields, auth failure (failing connect token), fingerprint mismatch (returning `fingerprintMismatchError` from `buildTLSConfig`), timeout (handler never called, context cancelled).
- Command layer tests use a stub `driver.Driver` (like `stubDriver` from add tests) returning preset `*StatusResult` or errors.
- All tests use `t.Setenv("POLIMERO_CONFIG_DIR", t.TempDir())` for config isolation.
- Hardware tests are out of scope for this plan.

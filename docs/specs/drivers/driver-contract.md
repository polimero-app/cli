# Driver Contract Spec

## Status

Accepted

## Purpose

Define the brand-neutral contract that CLI commands use to interact with printer drivers.

## Driver Identity

Each driver exposes:

- Stable driver name, such as `bambu-lan`.
- Human display name.
- Supported profile fields.
- Required secret kinds.
- Capability metadata.

## Context And Cancellation

Every operation that may block must accept `context.Context`.

Drivers must:

- Stop network work when context is canceled.
- Respect command timeouts.
- Avoid goroutine leaks.
- Return timeout errors that can be mapped to exit code `4`.

## Capabilities

Drivers declare capabilities before command execution when possible. Capabilities are expressed as a struct with bool fields:

```
Capabilities {
    Status           bool
    Discovery        bool
    FileList         bool
    FileDownload     bool
    FileUpload       bool
    JobUpload        bool
    JobStart         bool
    JobPause         bool
    JobResume        bool
    JobCancel        bool
    TemperatureRead  bool
    TemperatureWrite bool
    MotionControl    bool
    FanControl       bool
    LightControl     bool
    SpeedControl     bool
    TLSRefresh       bool
    CameraStream     bool
    CameraSnapshot   bool
}
```

Unsupported operations must return `unsupported_capability`.

## Profile Input

Drivers receive validated, non-secret profile data:

- profile name
- driver name
- host
- timeout
- insecure (bool)
- driver-specific fields (e.g. `serial` for `bambu-lan`), as declared in each driver spec

The `insecure` flag controls whether the driver skips transport certificate verification. Drivers must not silently ignore it.

Drivers must not access fields that are absent for their driver. Each driver spec enumerates its required and optional profile fields.

Drivers do not read Viper config directly. Profiles are validated by the command layer before being passed to the driver; drivers will not receive a profile for a name that does not exist.

## Secrets

The command layer resolves all required secrets from the OS keychain before calling the driver. Drivers receive a pre-resolved secrets bundle struct:

```
SecretsBundle {
    AccessCode      string  // printer access code; empty string if not applicable
    TLSFingerprint  string  // pinned TLS fingerprint, e.g. "sha256:aabb..."; empty if insecure mode
}
```

Drivers never access the keychain directly. All keychain I/O happens in the command layer.

Drivers must not:

- Read secrets from YAML config.
- Log secrets.
- Persist secrets.
- Return secrets in errors.

## Protocol Trace Diagnostics

Commands may attach an optional protocol trace sink to `context.Context` when the user passes `--protocol-trace <file>`.

Drivers must:

- Treat an absent trace sink as a no-op.
- Emit only sanitized JSON-serializable event fields.
- Use stable event names for protocol phases, parser decisions, fallback decisions, warnings, and sanitized errors.
- Include enough safe metadata for debugging: driver name, operation, phase, transport, duration, byte counts, selected protocol, capability decisions, response key inventories, safe scalar summaries, and secret-free protocol payloads (for example MQTT command/report JSON or discovery records) per ADR 0013.
- Respect context cancellation; trace emission must not keep protocol operations alive after cancellation.

Drivers must not:

- Change operation method signatures only to pass trace state.
- Require a trace sink for normal operation.
- Emit access codes, passwords, tokens, private keys, TLS private material, raw authentication payloads, protocol payloads containing credential material, raw file contents, or raw camera frames.
- Emit unsanitized backend, parser, TLS, FTP, MQTT, RTSP, discovery, or secret-store errors.
- Treat trace emission failure as recoverable inside the driver; write and close failures are owned by the command layer.

Trace events are diagnostics, not driver results. A driver must still return typed results, typed errors, and partial-data warnings through the normal operation contract.

## Status Operation

The driver-neutral status operation returns:

- state
- temperatures
- job summary
- progress
- active errors
- warnings
- capabilities

Partial status is allowed if optional fields fail. The driver must report partial-data warnings.

## Discovery Operation

Drivers that declare `Discovery: true` in their `Capabilities` must implement:

```go
Discover(ctx context.Context) ([]DiscoveredPrinter, error)
```

Where `DiscoveredPrinter` is:

```go
type DiscoveredPrinter struct {
    Host   string // IP address of the discovered printer
    Port   int    // service port from the mDNS SRV record (e.g. 8883)
    Serial string // serial number from service metadata; empty if absent
    Model  string // model identifier from service metadata; empty if absent
    Name   string // friendly name from service metadata; empty if absent
    Driver string // driver name (e.g. "bambu-lan"), populated by the driver
}
```

Contract:

- `ctx` deadline controls the scan duration; stop listening when context is done.
- Return a non-nil empty slice when no printers are found.
- Return exit code `4` if the mDNS socket cannot be opened.
- Do not connect to any printer during discovery.
- Do not read or access any secrets.

## File Operations

Drivers that declare any file capability in their `Capabilities` must expose file roots. Drivers that declare `FileList: true`, `FileDownload: true`, or `FileUpload: true` must implement the corresponding operation.

File operation input:

- Validated profile input.
- Pre-resolved secrets bundle.
- Validated root name.
- Normalized path within the root.
- Logger provided by the command layer.
- `context.Context` controlling the operation timeout.

Portable root and entry types:

```go
type DeviceFileRoot struct {
    Name          string
    Description   string
    Writable      bool
    CapacityBytes *int64
    FreeBytes     *int64
    Metadata      map[string]any
}

type DeviceFileEntry struct {
    Name       string
    Root       string
    Path       string
    Type       string         // "directory", "file", or "unknown"
    SizeBytes  *int64         // nil when unavailable or not applicable
    ModifiedAt *time.Time     // nil when unavailable
    Metadata   map[string]any // driver-specific fields for JSON output
}

type DeviceFileListing struct {
    Root         string
    Path         string
    Entries      []DeviceFileEntry
    Warnings     []StatusWarning
    Capabilities Capabilities
}

type DeviceFileTransferResult struct {
    Root             string
    Source           string
    Destination      string
    BytesTransferred *int64
    Warnings         []StatusWarning
    Capabilities     Capabilities
}
```

Contract:

- `FileList` lists one directory or one file per call.
- `FileDownload` downloads one regular file per call.
- `FileUpload` uploads one regular file per call.
- Upload operations store files only and must not start prints.
- Return a non-nil empty `Entries` slice when the directory has no visible entries.
- Do not delete, rename, move, create directories, or start prints.
- Respect context cancellation and command timeouts.
- Return `unsupported_capability` when the driver does not support the requested file operation.
- Return a typed path error when the normalized root/path does not exist or is incompatible with the requested operation.
- Sanitize transport and parser errors before they reach CLI output.
- Drivers may log requested paths and transfer summaries but must not log file contents or secrets.

## Camera Stream Operation

Drivers that declare `CameraStream: true` in their `Capabilities` must implement:

```go
CameraStream(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (*CameraStreamResult, error)
```

Where:

```go
type CameraStreamResult struct {
    Format       CameraFormat  // CameraFormatMJPEG or CameraFormatH264
    Stream       io.ReadCloser // raw bytes: MJPEG multipart or H.264 Annex-B
    Capabilities Capabilities
}

type CameraFormat string

const (
    CameraFormatMJPEG CameraFormat = "mjpeg"
    CameraFormatH264  CameraFormat = "h264"
)
```

Contract:

- The driver opens the TLS connection to the camera endpoint and returns an `io.ReadCloser` over the raw stream. The command layer owns the HTTP server; the driver does not know about HTTP.
- `ctx` controls the camera connection lifetime; close the stream when context is canceled.
- `Format` must be set before returning so the command layer can select the correct `Content-Type`.
- The driver must use the same pinned TLS fingerprint as for the MQTT connection. No additional keychain entry is required.
- `CameraFormatMJPEG`: raw `multipart/x-mixed-replace` MJPEG byte stream (A1/A1 mini families, port 6000).
- `CameraFormatH264`: raw H.264 Annex-B byte stream (X1/P1/P2/H-series/X2D families, port 322).
- Return `unsupported_capability` when the driver does not support camera streaming.
- Sanitize transport, TLS, and camera protocol errors before returning them.
- Do not log camera payload contents or secrets.

## Camera Snapshot Operation

Drivers that declare `CameraSnapshot: true` in their `Capabilities` must implement:

```go
CameraSnapshot(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (*CameraSnapshotResult, error)
```

Where:

```go
type CameraSnapshotResult struct {
    Data         []byte       // JPEG-encoded image
    Protocol     string       // "mjpeg" or "h264" (source protocol used)
    Capabilities Capabilities
}
```

Contract:

- The driver connects to the camera endpoint, captures a single frame, and returns JPEG-encoded bytes.
- `ctx` controls the connection and frame capture lifetime; abort if context is canceled or deadline exceeded.
- For MJPEG endpoints: read one frame from the proprietary format (already JPEG) and return it directly.
- For H.264/RTSPS endpoints: connect, start decoding at a keyframe with codec parameters, continue feeding later access units until a decoded image is available, encode as JPEG, and return.
- The driver must use the same pinned TLS fingerprint as for the MQTT and camera stream connections. No additional keychain entry is required.
- Return `unsupported_capability` when the driver does not support camera snapshot.
- Sanitize transport, TLS, decode, and encode errors before returning them.
- Do not log camera image data or secrets.

## Job Control Operations

Drivers that declare `JobStart`, `JobPause`, `JobResume`, or `JobCancel` in their `Capabilities` must implement the corresponding operation:

```go
JobStart(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, devicePath string, opts JobStartOptions) (JobActionResult, error)
JobPause(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (JobActionResult, error)
JobResume(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (JobActionResult, error)
JobCancel(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (JobActionResult, error)
```

Where:

```go
type JobStartOptions struct {
    Plate        *int // nil means driver/printer default (e.g. first or only plate)
    SkipLeveling bool
}

type JobActionResult struct {
    State        string // resulting portable state ("idle", "printing", "paused", "error", "unknown"), confirmed
    Warnings     []StatusWarning
    Capabilities Capabilities
}
```

Contract:

- The command layer checks the state precondition (via the status operation) and obtains operator confirmation before calling any job action method. Drivers do not re-derive or enforce these business-level preconditions themselves.
- `devicePath` passed to `JobStart` is already validated and normalized by the command layer, identical to file operation paths.
- Each method sends the action to the printer and then blocks, bounded by `ctx`, until it can confirm the resulting state from the printer rather than merely that the command was transmitted.
- If the confirmed resulting state contradicts the expected transition for that action (`JobStart`/`JobResume` → `printing`, `JobPause` → `paused`, `JobCancel` → `idle`), return `job_action_failed` rather than a result indicating success.
- Return `timeout` if no confirming state update arrives before the context deadline.
- Return `unsupported_capability` when the driver does not support the requested action.
- `JobStart` must not implicitly upload a file; the file must already exist on printer storage.
- Sanitize transport and protocol errors before returning them.
- Drivers may log the action name and resulting state but must not log secrets or raw protocol payloads.

## Temperature Control Operation

Drivers that declare `TemperatureWrite: true` in their `Capabilities` must implement:

```go
TemperatureSet(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, targets TemperatureTargets) (TemperatureResult, error)
```

Where:

```go
type TemperatureTargets struct {
    NozzleCelsius  *float64 // nil means "leave unchanged"
    BedCelsius     *float64
    ChamberCelsius *float64
}

type TemperatureResult struct {
    Targets      TemperatureTargets // acknowledged targets, as confirmed by the printer
    Warnings     []StatusWarning
    Capabilities Capabilities
}
```

Contract:

- The command layer enforces generic safety bounds (nozzle 0–300°C, bed 0–120°C, chamber 0–65°C) and the `idle`-state precondition before calling this operation. Drivers do not re-derive these checks.
- A target of `0` means "turn the heater off."
- The driver blocks, bounded by `ctx`, until the printer acknowledges the new target value(s) — not until the current temperature reaches target.
- `TemperatureWrite: true` means the driver supports the temperature-set protocol generally. If the connected model does not have a specific heater (e.g. no chamber heater), return `unsupported_capability` for that specific request rather than assuming uniform hardware across the driver's supported models.
- Return `timeout` if no acknowledgment arrives before the context deadline.
- Sanitize transport and protocol errors before returning them.

## Motion Control Operation

Drivers that declare `MotionControl: true` in their `Capabilities` must implement:

```go
MotionHome(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, axes []Axis) (MotionResult, error)
MotionJog(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, delta JogDelta) (MotionResult, error)
```

Where:

```go
type Axis string

const (
    AxisX Axis = "x"
    AxisY Axis = "y"
    AxisZ Axis = "z"
)

type JogDelta struct {
    XMillimeters     *float64 // nil means "do not move this axis"
    YMillimeters     *float64
    ZMillimeters     *float64
    FeedrateMmPerMin int
}

type MotionResult struct {
    State        MotionState // "accepted" or "complete"
    Warnings     []StatusWarning
    Capabilities Capabilities
}

type MotionState string

const (
    MotionStateAccepted MotionState = "accepted"
    MotionStateComplete MotionState = "complete"
)
```

Contract:

- The command layer enforces the `idle`-state precondition and the generic jog distance bound (±10mm per axis per call) before calling either method. Drivers do not re-derive these checks.
- `MotionHome` with an empty `axes` slice homes all axes.
- The driver blocks, bounded by `ctx`, until it can return a truthful result state:
  - `complete`: the driver confirmed the requested motion has physically finished.
  - `accepted`: the driver confirmed the motion command was accepted by the printer and a fresh status channel is alive, but the protocol does not expose a reliable physical completion signal.
- Drivers must not return `complete` from send-only or generic status-echo acknowledgments.
- Return `timeout` if no result-state confirmation arrives before the context deadline.
- Return `unsupported_capability` when the driver does not support the requested axis or motion type.
- Sanitize transport and protocol errors before returning them.

## Fan Control Operation

Drivers that declare `FanControl: true` in their `Capabilities` must implement:

```go
FanSet(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, target FanTarget) (FanControlResult, error)
```

Where:

```go
type FanTarget struct {
    Fan          string // canonical driver-supported fan key
    SpeedPercent int    // 0-100, where 0 means off
}

type FanControlResult struct {
    Fan          string // canonical fan key acknowledged by the printer
    SpeedPercent int
    Warnings     []StatusWarning
    Capabilities Capabilities
}
```

Contract:

- The command layer enforces fan speed bounds (`0` to `100`) and the state
  precondition (`idle`, `printing`, or `paused`) before calling this operation.
  Drivers do not re-derive these checks.
- The command layer normalizes portable aliases before dispatch. Drivers receive
  canonical fan keys and must return canonical fan keys in results.
- The operation must only expose user-controllable fans. Firmware-managed safety
  fans such as heatbreak, hotend, controller, electronics, and power-supply fans
  are not generic fan-control targets.
- The driver blocks, bounded by `ctx`, until the printer acknowledges the
  requested fan speed. A fresh status echo is preferred. An explicit protocol
  acknowledgment is acceptable only when it identifies the requested fan and
  requested percentage. Transport publish or socket-write success alone is not
  an acknowledgment.
- If the connected model does not support the requested fan, return
  `unsupported_capability`.
- Return `timeout` if no acknowledgment arrives before the context deadline.
- Sanitize transport and protocol errors before returning them.

## Light Control Operation

Drivers that declare `LightControl: true` in their `Capabilities` must
implement:

```go
LightSet(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, target LightTarget) (LightControlResult, error)
```

Where:

```go
type LightState string

const (
    LightStateOn  LightState = "on"
    LightStateOff LightState = "off"
)

type LightTarget struct {
    Light string
    State LightState
}

type LightControlResult struct {
    Light        string
    State        LightState
    Warnings     []StatusWarning
    Capabilities Capabilities
}
```

Contract:

- The command layer enforces the known-reachable-state precondition before
  calling this operation. Allowed states are `idle`, `printing`, `paused`, and
  `error`.
- The command layer normalizes portable aliases before dispatch. Drivers receive
  canonical light keys and must return canonical light keys in results.
- This operation controls on/off lighting only. Brightness, color, animation,
  and scheduled lighting require a later contract.
- The driver blocks, bounded by `ctx`, until the printer acknowledges the
  requested light state. A fresh status echo is preferred. An explicit protocol
  acknowledgment is acceptable only when it identifies the requested light and
  requested state. Transport publish or socket-write success alone is not an
  acknowledgment.
- If the connected model does not support the requested light, return
  `unsupported_capability`.
- Return `timeout` if no acknowledgment arrives before the context deadline.
- Sanitize transport and protocol errors before returning them.

## Speed Control Operation

Drivers that declare `SpeedControl: true` in their `Capabilities` must
implement:

```go
SpeedSet(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger, profile string) (SpeedControlResult, error)
```

Where:

```go
type SpeedControlResult struct {
    SpeedProfile string
    Warnings     []StatusWarning
    Capabilities Capabilities
}
```

Contract:

- The command layer enforces profile token syntax and the state precondition
  (`printing` or `paused`) before calling this operation.
- Speed profiles are stable lowercase string tokens documented by the driver
  spec. Drivers receive canonical speed profile tokens and must return canonical
  tokens in results.
- This operation controls active print speed profile only. It must not expose
  arbitrary G-code, percentage multipliers, acceleration, jerk, pressure
  advance, flow, or motion feedrate settings.
- The driver blocks, bounded by `ctx`, until the printer acknowledges the
  requested speed profile. A fresh status echo is preferred. An explicit
  protocol acknowledgment is acceptable only when it identifies the requested
  profile. Transport publish or socket-write success alone is not an
  acknowledgment.
- If the connected model or firmware does not support the requested profile,
  return `unsupported_capability`.
- Return `timeout` if no acknowledgment arrives before the context deadline.
- Sanitize transport and protocol errors before returning them.

## Errors

Drivers return typed errors using internal error codes. The command layer maps these to the public JSON `error.code` values emitted in `--output json` responses. These are two distinct namespaces; the mapping is:

| Driver internal code | Public JSON `error.code` |
|---|---|
| `invalid_profile` | `invalid_profile` |
| `secret_not_found` | `secret_not_found` |
| `authentication_failed` | `authentication_failed` |
| `connection_failed` | `connection_failed` |
| `device_path_not_found` | `device_path_not_found` |
| `device_path_not_directory` | `device_path_not_directory` |
| `device_path_not_file` | `device_path_not_file` |
| `device_path_exists` | `device_path_exists` |
| `device_storage_rejected` | `device_storage_rejected` |
| `timeout` | `timeout` |
| `unsupported_capability` | `capability_unsupported` |
| `invalid_printer_state` | `invalid_printer_state` |
| `job_action_failed` | `job_action_failed` |
| `unsafe_value` | `unsafe_value` |
| `driver_internal_error` | `internal_error` |

The command layer is responsible for the translation. Driver implementations must use the internal codes only.

Command-layer validation failures that occur before driver dispatch are outside
the driver internal-code namespace. For example, auxiliary-control command specs
use public JSON error code `invalid_argument` for malformed CLI tokens that
drivers never receive.

Partial data is reported as warnings in the status result, not as an error. Errors must be sanitized before they reach CLI output.

## Logging

Drivers may emit structured logs through a `*slog.Logger` provided by the command layer. Drivers must not create their own logger instances.

Logs must:

- Include driver name and operation.
- Exclude secrets.
- Exclude sensitive protocol payloads.
- Redact host details only if future privacy settings require it.

## Testing

Every driver must support:

- Unit tests with mock transport.
- Contract tests for capability handling.
- Contract tests for context cancellation.
- Contract tests for error mapping.
- Contract tests for redaction.
- Contract tests for file capability handling, path errors, empty directory results, transfer overwrite handling, and secret redaction when file operations are supported.
- Contract tests for job/temperature/motion capability handling, confirmed-state-vs-expected-transition mismatches (`job_action_failed`), and timeout while awaiting confirmation, when these capabilities are supported.
- Contract tests for fan/light/speed capability handling, unsupported targets,
  timeout while awaiting acknowledgment, and secret redaction when these
  capabilities are supported.

Hardware tests must be opt-in and build-tagged.

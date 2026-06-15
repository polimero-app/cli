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
    JobCancel        bool
    TemperatureRead  bool
    TemperatureWrite bool
    MotionControl    bool
    TLSRefresh       bool
    CameraStream     bool
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
CameraStream(ctx context.Context, p ProfileInput, s SecretsBundle, log *slog.Logger) (CameraStreamResult, error)
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

## Errors

Drivers return typed errors that map to public error codes:

- `invalid_profile`
- `secret_not_found`
- `authentication_failed`
- `connection_failed`
- `device_path_not_found`
- `device_path_not_directory`
- `device_path_not_file`
- `device_path_exists`
- `device_storage_rejected`
- `timeout`
- `unsupported_capability`
- `driver_internal_error`

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

Hardware tests must be opt-in and build-tagged.

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
    JobUpload        bool
    JobStart         bool
    JobPause         bool
    JobCancel        bool
    TemperatureRead  bool
    TemperatureWrite bool
    MotionControl    bool
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

## Errors

Drivers return typed errors that map to public error codes:

- `invalid_profile`
- `secret_not_found`
- `authentication_failed`
- `connection_failed`
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

Hardware tests must be opt-in and build-tagged.


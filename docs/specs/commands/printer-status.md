# Command Spec: `printer status`

## Status

Accepted

## Purpose

Query current status from a configured printer through the driver-neutral status contract.

## Syntax

```text
polimero printer status <name> [--timeout <duration>] [--output <format>]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--timeout <duration>`: optional. Overrides profile/default timeout for this command.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads the named profile from versioned YAML config under `os.UserConfigDir`.

The profile must include:

- name
- driver
- host
- serial (used by the driver for TLS SNI and MQTT topic construction)
- timeout or default timeout

## Secret Requirements

The command loads keychain entries using the driver name and profile name from the stored profile:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint` (skipped when `--insecure` or `profile.insecure: true`)

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

## Behavior

- The command is read-only.
- The command dispatches through the driver-neutral status interface.
- Default timeout is `10s`.
- No retry is performed by default.
- Partial status is allowed when optional fields cannot be retrieved.
- Partial status must include warnings.
- Unsupported driver capabilities fail with exit code `5`.

## Status Data Contract

Portable status fields:

- `profile`: profile name.
- `driver`: driver name.
- `state`: one of `unknown`, `offline`, `idle`, `printing`, `paused`, `error`. `offline` means the connection attempt failed or timed out (printer is unreachable). `unknown` means the driver connected but could not determine a clear state from the response.
- `temperatures`: available nozzle, bed, chamber, and target temperatures. `null` if unavailable.
- `job`: active job summary when available. `null` if no active job or unavailable.
- `progress`: percentage and time estimates when available. `null` if unavailable.
- `errors`: active printer errors. Always an array; empty when no active errors. Each element is an object with `code` (string) and `message` (string).
- `warnings`: partial-data or non-fatal retrieval warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

Drivers may include adapter-specific raw fields only inside a namespaced extension object. The first implementation should avoid extensions unless required for debugging a documented gap.

## Output

Human success example:

```text
Printer: garage-x1c
State: printing
Progress: 42%
Nozzle: 215.0 C / 220.0 C
Bed: 60.0 C / 60.0 C
Job: bracket.3mf
```

Human partial-data example:

```text
Printer: garage-x1c
State: printing
Progress: 42%
Warnings:
- chamber temperature unavailable
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "state": "printing",
    "temperatures": {
      "nozzle": {
        "currentCelsius": 215.0,
        "targetCelsius": 220.0
      },
      "bed": {
        "currentCelsius": 60.0,
        "targetCelsius": 60.0
      }
    },
    "job": {
      "name": "bracket.3mf"
    },
    "progress": {
      "percent": 42
    },
    "errors": [],
    "warnings": [],
    "capabilities": {
      "status": true
    }
  },
  "error": null,
  "meta": {
    "command": "printer status",
    "durationMs": 148
  }
}
```

JSON timeout example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "timeout",
    "message": "printer status request timed out",
    "details": {
      "profile": "garage-x1c",
      "timeout": "10s"
    }
  },
  "meta": {
    "command": "printer status"
  }
}
```

## Exit Codes

- `0`: status retrieved, including partial status with warnings.
- `1`: general failure.
- `2`: usage, profile, config, or validation error.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability.

## Error Cases

- Missing `<name>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Authentication failed.
- Connection failed.
- Timeout.
- Driver does not support status.
- Driver returns malformed status.

## Security Requirements

- Do not print or log access codes.
- Do not include protocol payloads in debug logs unless redacted.
- Do not perform discovery or scanning.
- Do not send state-changing commands.
- Sanitize authentication and transport errors.

## Test Scenarios

- Returns full status for a mock driver.
- Returns partial status with warnings.
- Fails when profile is missing.
- Fails when access code is missing from keychain.
- Fails when TLS fingerprint is missing for a secure profile.
- Fails with exit code `3` on TLS fingerprint mismatch (TOFU violation).
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with auth error.
- Fails with timeout.
- Fails with unsupported capability.
- Uses command timeout override.
- Emits stable JSON envelope.
- Does not leak secrets in output or logs.

## Non-goals

- Starting, pausing, canceling, or uploading jobs.
- Discovering printers.
- Showing Bambu cloud state.
- Retrying transient failures.


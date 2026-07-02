# Command Spec: `printer tls refresh`

## Status

Accepted

## Purpose

Re-pin the TLS certificate fingerprint for a configured printer profile without modifying the access code or other profile settings. Used when a printer's certificate changes legitimately, such as after a firmware update.

## Syntax

```text
polimero printer tls refresh <name> [--timeout <duration>] [--insecure] [--yes] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--timeout <duration>`: optional. Overrides the profile/default timeout for this connection.
- `--insecure`: skip TLS verification for this connection. Updates the profile to `insecure: true` and removes the stored fingerprint from the keychain.
- `--yes`: bypass interactive confirmation.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol diagnostics to a new local file. The file must not already exist. Trace output may include connection phase, SNI presence, captured fingerprint format status, durations, and sanitized TLS error categories. It must not include TLS private material, raw certificate data, access codes, keychain backend errors, or unsanitized TLS errors.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads the named profile from versioned YAML config under `os.UserConfigDir`.

After a successful re-pin, the command updates the profile:

- If `--insecure` is not passed: sets `insecure: false` and updates `updated` timestamp.
- If `--insecure` is passed: sets `insecure: true` and updates `updated` timestamp.

## Secret Requirements

The command does not read or modify the access code.

Without `--insecure`: stores the new fingerprint in OS keychain:

- Service: `polimero`
- Account: `bambu-lan:<name>:tls-fingerprint`

With `--insecure`: removes the existing `bambu-lan:<name>:tls-fingerprint` keychain entry if present, then stores nothing. The config update is the primary effect: if the profile was saved as insecure but the keychain delete fails, the command still succeeds and reports a `tls_fingerprint_delete_failed` warning (matching `printer remove`), since the stale entry has no security effect and rerunning cannot roll back the config.

If the keychain is unavailable, the command fails with exit code `3`.

Keychain fingerprint writes and deletes use the same bounded timeout as the TLS refresh connection and must not expose raw secret-store backend errors.

## Confirmation

When running interactively and `--yes` is not provided, prompt:

```text
Re-pin TLS certificate for <name>? Type 'yes' to continue:
```

Only `yes` is accepted. Any other input aborts with exit code `2`.

In non-interactive mode, `--yes` is required. Without it, the command fails with exit code `2`.

## Behavior

- The command connects to the printer solely to obtain the current TLS certificate.
- The TLS SNI field is set to the `serial` value from the stored profile. This is required for Bambu printers, whose certificate CN equals the printer serial number.
- The captured fingerprint must be formatted as `sha256:<64 lowercase hex characters>` before it is stored.
- No state-changing commands are sent to the printer.
- The command does not use the stored access code for the TLS handshake; only a TLS connection is needed to capture the leaf certificate fingerprint.
- Default timeout is `10s`.
- No retry.
- When `--protocol-trace` is set, the trace file is created before the TLS connection or insecure-skip decision and closed before command exit. If the trace file cannot be created, the command fails before protocol work with exit code `2`. If trace writing or closing fails after protocol work starts, the command fails with exit code `1` unless an earlier, more specific failure already occurred. With `--insecure`, the trace may record that fingerprint capture was skipped.

## Output

Human success example:

```text
TLS certificate re-pinned: garage-x1c
Fingerprint: sha256:aabbcc...
```

Human insecure example:

```text
TLS certificate verification disabled: garage-x1c
Warning: TLS verification is disabled for this profile.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "fingerprint": "sha256:aabbcc...",
    "insecure": false
  },
  "error": null,
  "meta": {
    "command": "printer tls refresh",
    "durationMs": 48
  }
}
```

When `--protocol-trace` is enabled and the trace file is opened successfully, JSON `meta` may include `protocolTracePath`. Human output never includes trace contents.

JSON insecure example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "fingerprint": null,
    "insecure": true
  },
  "error": null,
  "meta": {
    "command": "printer tls refresh"
  }
}
```

When the profile was switched to insecure but the stored fingerprint could not be deleted, `data` additionally contains a `warnings` array with entries of the form `{"code": "tls_fingerprint_delete_failed", "message": "..."}`; human output prints a corresponding `Warning:` line.

JSON error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "connection_failed",
    "message": "could not connect to printer",
    "details": {
      "profile": "garage-x1c"
    }
  },
  "meta": {
    "command": "printer tls refresh"
  }
}
```

## Exit Codes

- `0`: fingerprint updated or insecure mode set.
- `1`: general failure, including trace write or close failure after protocol work starts.
- `2`: usage, confirmation, config, missing profile error, or invalid/uncreatable protocol trace path before protocol work starts.
- `3`: secret-store error.
- `4`: connection or TLS error.

## Error Cases

- Invalid profile name.
- Profile not found.
- Confirmation declined.
- Non-interactive execution without `--yes`.
- Connection failed.
- Timeout.
- Invalid captured fingerprint.
- Protocol trace path already exists or cannot be created.
- Secret-store unavailable.
- Fingerprint write fails.
- Fingerprint removal fails when switching to insecure mode.
- Config write failure.

## Security Requirements

- Do not read or log the access code.
- Do not send state-changing commands to the printer.
- Use a bounded network timeout.
- Use bounded keychain operations.
- Protocol trace output must contain sanitized TLS summaries only. It must not include TLS private material, raw certificate data, access codes, keychain backend errors, or unsanitized TLS errors.
- Sanitize TLS, connection, and secret-store errors before output.

## Test Scenarios

- Re-pins fingerprint for a mock driver.
- Updates `updated` timestamp in profile after re-pin.
- Switches insecure profile to secure: stores new fingerprint, sets `insecure: false`.
- Switches secure profile to insecure: removes fingerprint from keychain, sets `insecure: true`.
- Succeeds with `tls_fingerprint_delete_failed` warning when the config was switched to insecure but the keychain delete fails.
- Requires confirmation interactively.
- Requires `--yes` in non-interactive mode.
- Rejects missing profile.
- Fails with connection error.
- Fails with timeout.
- Emits stable JSON envelope for success and failure.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Fails before connecting when the protocol trace file cannot be created.
- Does not leak TLS private material, raw certificate data, access codes, or keychain backend details in protocol trace output.
- Shows warning in human output when switching to insecure mode.
- Does not touch the access code keychain entry.

## Non-goals

- Changing the access code or other profile settings.
- Validating that the access code still works after re-pinning.
- Overwriting a profile's host or timeout settings.

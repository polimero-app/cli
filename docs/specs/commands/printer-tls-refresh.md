# Command Spec: `printer tls refresh`

## Status

Accepted

## Purpose

Re-pin the TLS certificate fingerprint for a configured printer profile without modifying the access code or other profile settings. Used when a printer's certificate changes legitimately, such as after a firmware update.

## Syntax

```text
polimero printer tls refresh <name> [--timeout <duration>] [--insecure] [--yes]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--timeout <duration>`: optional. Overrides the profile/default timeout for this connection.
- `--insecure`: skip TLS verification for this connection. Updates the profile to `insecure: true` and removes the stored fingerprint from the keychain.
- `--yes`: bypass interactive confirmation.

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

With `--insecure`: removes the existing `bambu-lan:<name>:tls-fingerprint` keychain entry if present, then stores nothing.

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
- `1`: general failure.
- `2`: usage, confirmation, config, or missing profile error.
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
- Secret-store unavailable.
- Fingerprint write fails.
- Fingerprint removal fails when switching to insecure mode.
- Config write failure.

## Security Requirements

- Do not read or log the access code.
- Do not send state-changing commands to the printer.
- Use a bounded network timeout.
- Use bounded keychain operations.
- Sanitize TLS, connection, and secret-store errors before output.

## Test Scenarios

- Re-pins fingerprint for a mock driver.
- Updates `updated` timestamp in profile after re-pin.
- Switches insecure profile to secure: stores new fingerprint, sets `insecure: false`.
- Switches secure profile to insecure: removes fingerprint from keychain, sets `insecure: true`.
- Requires confirmation interactively.
- Requires `--yes` in non-interactive mode.
- Rejects missing profile.
- Fails with connection error.
- Fails with timeout.
- Emits stable JSON envelope for success and failure.
- Shows warning in human output when switching to insecure mode.
- Does not touch the access code keychain entry.

## Non-goals

- Changing the access code or other profile settings.
- Validating that the access code still works after re-pinning.
- Overwriting a profile's host or timeout settings.

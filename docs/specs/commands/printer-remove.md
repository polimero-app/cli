# Command Spec: `printer remove`

## Status

Accepted

## Purpose

Remove a named printer profile and its associated secret.

## Syntax

```text
polimero printer remove <name> [--yes] [--output <format>]
```

## Arguments

- `<name>`: existing printer profile name.

## Flags

- `--yes`: bypass interactive confirmation.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command removes the named profile from versioned YAML config under `os.UserConfigDir`.

## Secret Requirements

The command removes both keychain entries using the driver name and profile name from the stored profile:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint`

If either entry is missing from the keychain, the command still removes the profile and returns a separate warning per missing entry. If the profile was created with `--insecure`, the TLS fingerprint entry will not be present; this is not a warning condition.

Before mutating state, the command reads the existing entries so they can be restored if a later step fails. It deletes present secrets before saving the profile removal. An operational keychain deletion failure prevents profile removal and returns exit code `3`; any secret already deleted during that attempt is restored before returning. If the config save fails after deletion, the command restores both secrets before returning the config error.

Keychain reads, deletions, and rollback writes use bounded internal timeouts and must not expose raw secret-store backend errors or secret values.

## Confirmation

When running interactively and `--yes` is not provided, prompt:

```text
Remove printer profile <name> and its stored secrets? Type 'yes' to continue:
```

Only `yes` is accepted.

In non-interactive mode, `--yes` is required. Without `--yes`, the command fails with exit code `2`.

## Output

Human success example:

```text
Printer profile removed: garage-x1c
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "removed": {
      "name": "garage-x1c",
      "accessCodeRemoved": true,
      "tlsFingerprintRemoved": true
    },
    "warnings": []
  },
  "error": null,
  "meta": {
    "command": "printer remove"
  }
}
```

JSON success example with warnings (access code missing):

```json
{
  "ok": true,
  "data": {
    "removed": {
      "name": "garage-x1c",
      "accessCodeRemoved": false,
      "tlsFingerprintRemoved": true
    },
    "warnings": [
      {
        "code": "access_code_not_found",
        "message": "profile was removed, but no stored access code was found"
      }
    ]
  },
  "error": null,
  "meta": {
    "command": "printer remove"
  }
}
```

## Exit Codes

- `0`: profile removed.
- `1`: general failure.
- `2`: usage, confirmation, config, or missing profile error.
- `3`: secret-store error that prevents removal.

## Error Cases

- Invalid profile name.
- Too many arguments.
- Profile not found.
- Confirmation declined.
- Non-interactive execution without `--yes`.
- Config read or write failure.
- Secret-store unavailable.
- Secret deletion failure other than not found.

## Security Requirements

- Never print or log secret values.
- Remove the secret before or during profile removal.
- Use bounded keychain operations.
- If config writing or a later secret deletion fails, restore any secrets already deleted before returning an error.
- Use atomic config writes where practical.

## Test Scenarios

- Removes profile, access code, and TLS fingerprint.
- Removes profile when access code is missing; returns `access_code_not_found` warning.
- Removes profile when TLS fingerprint is missing (insecure profile); no warning (expected absence).
- Removes profile when TLS fingerprint is missing (secure profile that lost its entry); returns `tls_fingerprint_not_found` warning.
- Rejects missing profile.
- Requires confirmation interactively.
- Requires `--yes` in non-interactive mode.
- Emits stable JSON envelope with granular `accessCodeRemoved` and `tlsFingerprintRemoved` fields.
- Sanitizes secret-store errors.
- Keeps the profile and restores prior secrets when an operational keychain deletion fails.
- Restores deleted secrets when the config save fails.

## Non-goals

- Removing discovered but unconfigured printers.
- Bulk removal.
- Secret rotation.

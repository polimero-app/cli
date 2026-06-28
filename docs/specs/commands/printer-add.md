# Command Spec: `printer add`

## Status

Accepted

## Purpose

Create a named printer profile and store any required printer secret in the OS keychain.

## Syntax

```text
polimero printer add <name> --driver <driver> --host <host> [--serial <serial>] [--timeout <duration>] [--access-code-file <path>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<name>`: stable profile name used by later commands.

Profile names are case-insensitive. Input is normalized to lowercase before validation and storage.

Profile names must:

- Be non-empty.
- Use only ASCII letters, digits, `.`, `_`, and `-`.
- Start with an ASCII letter or digit.
- Be at most 64 characters.

## Flags

- `--driver <driver>`: required. Initial accepted value: `bambu-lan`.
- `--host <host>`: required. IP address or DNS hostname. Must be a valid IP address or a conservative ASCII DNS hostname with labels separated by dots; labels must start and end with an ASCII letter or digit and may contain hyphens internally.
- `--serial <serial>`: required for `bambu-lan`. The printer's serial number, used for TLS SNI and MQTT topic construction. Stored verbatim in the profile; must be non-empty, printable ASCII with no whitespace, and at most 64 characters.
- `--timeout <duration>`: optional. Default: `10s`. Must parse as a Go duration and be greater than zero.
- `--access-code-file <path>`: optional. Reads a secret from a file.
- `--insecure`: optional. Skips TLS verification and MQTT auth check. Profile is stored with `insecure: true`. No TLS fingerprint is stored. Human output includes a warning.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol diagnostics to a new local file. The file must not already exist. Trace output may include TLS/MQTT connectivity phases, fingerprint capture status, auth result category, byte counts, durations, and sanitized error categories. It must not include the access code, raw MQTT auth payloads, TLS private material, keychain backend errors, or unsanitized transport errors.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

The command must not provide a `--access-code` flag.

## Config Requirements

The command writes non-secret profile data to versioned YAML under `os.UserConfigDir`.

Config file path: `polimero/polimero.yaml` under `os.UserConfigDir`. Profiles are stored as a map keyed by profile name.

Stored profile fields:

- config schema version
- profile name
- driver
- host
- serial (driver-specific; present for `bambu-lan`, omitted for drivers that do not require it)
- timeout
- insecure
- created timestamp
- updated timestamp

The config file must not contain the Bambu LAN access code or the TLS fingerprint.

## Secret Requirements

For `--driver bambu-lan`, an access code is required.

Secret input order:

1. If `--access-code-file` is provided, read from that file.
2. If running interactively on a TTY, prompt with hidden input.
3. If non-interactive and no file is provided, fail with exit code `2`.

TTY prompt text:

```text
Enter Bambu LAN access code for <name>:
```

`--access-code-file` requirements:

- Must refer to a regular file.
- Must not be a symbolic link on operating systems that support no-follow file opens.
- Must be readable by the current user.
- Must not exceed 4 KiB.
- On operating systems that expose POSIX-like file modes, group and other permissions must not grant read, write, or execute access.
- One trailing `\n` or `\r\n` is trimmed.
- Other leading or trailing whitespace is preserved.
- Empty access-code input after trailing newline trimming is rejected.
- The path may be shown in diagnostics, but file contents must never be logged or printed.

Keychain entries written by this command:

- Access code: service `polimero`, account `bambu-lan:<name>:access-code`.
- TLS fingerprint: service `polimero`, account `bambu-lan:<name>:tls-fingerprint`. Not written when `--insecure` is used.

If keychain storage is unavailable, the command fails closed. Keychain writes and rollback deletes must use bounded contexts and must not expose raw secret-store backend errors.

## Connection Requirements

The command performs a driver-defined connectivity check before storing the profile. The profile is not stored if the check fails.

For `--driver bambu-lan` (non-insecure): establishes a full MQTT connection over TLS to the printer. During the TLS handshake, the leaf certificate fingerprint is captured (Trust On First Use per ADR 0007). The MQTT CONNECT packet is sent with username `bblp` and the supplied access code as password. A non-zero CONNACK return code is treated as an authentication failure (exit code `3`). The `--insecure` flag skips TLS verification and the MQTT auth check entirely; the profile is stored without a fingerprint.

Other drivers define their own connectivity check and transport security requirements in their driver spec.

When `--protocol-trace` is set, the trace file is created before the connectivity check or insecure-skip decision and closed before command exit. If the trace file cannot be created, the command fails before protocol work with exit code `2`. If trace writing or closing fails after protocol work starts, the command fails with exit code `1` unless an earlier, more specific failure already occurred. With `--insecure`, the trace may record that the connectivity check was skipped, but it must not record the access code source contents or keychain backend details.

Execution order for `bambu-lan` (non-insecure):

1. Validate inputs (normalize name to lowercase, check format, check no duplicate, validate serial format).
2. Collect access code from file or TTY.
3. Establish MQTT connection over TLS to `<host>:8883` with SNI set to `<serial>`: skip TLS chain verification, capture the SHA-256 fingerprint of the leaf certificate, authenticate with username `bblp` and the collected access code. Fail with exit code `4` if the TLS or network connection fails; fail with exit code `3` if MQTT authentication is rejected.
4. Store access code in keychain.
5. Store TLS fingerprint in keychain.
6. Write profile YAML atomically.

Execution order for `bambu-lan` (with `--insecure`):

1. Validate inputs (normalize name to lowercase, check format, check no duplicate, validate serial format).
2. Collect access code from file or TTY.
3. Store access code in keychain.
4. Write profile YAML atomically with `insecure: true`.

Rollback (non-insecure):

- If step 5 (store fingerprint) fails after step 4 (store access code): remove the access code from keychain before returning error.
- If step 6 (write profile) fails after steps 4 and 5: remove both keychain entries before returning error.

Rollback (insecure):

- If step 4 (write profile) fails after step 3 (store access code): remove the access code from keychain before returning error.

## Output

Human success example:

```text
Printer profile added: garage-x1c
Driver: bambu-lan
Host: 192.0.2.10
Serial: 01S09C450100XXX
TLS: sha256:aabbcc...
```

Human insecure example:

```text
Printer profile added: garage-x1c
Driver: bambu-lan
Host: 192.0.2.10
Serial: 01S09C450100XXX
Warning: TLS verification is disabled for this profile.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": {
      "name": "garage-x1c",
      "driver": "bambu-lan",
      "host": "192.0.2.10",
      "serial": "01S09C450100XXX",
      "timeout": "10s",
      "insecure": false,
      "tlsFingerprint": "sha256:aabbcc..."
    }
  },
  "error": null,
  "meta": {
    "command": "printer add"
  }
}
```

When `--protocol-trace` is enabled and the trace file is opened successfully, JSON `meta` may include `protocolTracePath`. Human output never includes trace contents.

JSON insecure example:

```json
{
  "ok": true,
  "data": {
    "profile": {
      "name": "garage-x1c",
      "driver": "bambu-lan",
      "host": "192.0.2.10",
      "serial": "01S09C450100XXX",
      "timeout": "10s",
      "insecure": true,
      "tlsFingerprint": null
    }
  },
  "error": null,
  "meta": {
    "command": "printer add"
  }
}
```

JSON error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "profile_exists",
    "message": "printer profile already exists",
    "details": {
      "profile": "garage-x1c"
    }
  },
  "meta": {
    "command": "printer add"
  }
}
```

## Exit Codes

- `0`: profile and secret stored.
- `1`: general failure, including trace write or close failure after protocol work starts.
- `2`: usage, validation, config, duplicate profile error, or invalid/uncreatable protocol trace path before protocol work starts.
- `3`: authentication failure (MQTT auth rejected) or secret-store error.
- `4`: network or TLS connection failure.

## Error Cases

- Invalid profile name.
- Too many arguments.
- Unsupported driver.
- Missing host.
- Invalid host format.
- Missing serial for `bambu-lan`.
- Invalid serial format.
- Invalid timeout.
- Duplicate profile.
- Missing access code in non-interactive mode.
- Empty access code.
- Access-code file is missing, not regular, too large, or too broadly permissioned.
- Protocol trace path already exists or cannot be created.
- TLS or network connection failed (without `--insecure`).
- MQTT authentication rejected (bad access code).
- OS keychain unavailable.
- Access code keychain write fails.
- TLS fingerprint keychain write fails after access code write (rollback: remove access code).
- Config write fails after keychain writes (rollback: remove both keychain entries).

## Security Requirements

- Never print or log the access code.
- Never store the access code or TLS fingerprint in YAML config.
- Avoid command-line secret flags.
- Fail closed when the keychain is unavailable.
- Sanitize authentication, transport, and secret-store errors.
- Protocol trace output must contain sanitized event summaries only. It must not include the access code, raw auth payloads, TLS private material, raw MQTT payloads, keychain backend errors, or unsanitized transport errors.
- Use TOFU TLS per ADR 0007 unless `--insecure` is explicitly passed.
- Warn in human output when `--insecure` is used.
- Use atomic config writes where practical.
- Newly created config files should use owner-only permissions where the OS supports file permissions.

## Test Scenarios

- Adds valid Bambu LAN profile with access-code file; pins TLS fingerprint; MQTT auth succeeds.
- Adds valid Bambu LAN profile with hidden prompt; pins TLS fingerprint; MQTT auth succeeds.
- Adds profile with `--insecure`; no fingerprint stored; no MQTT connection attempted; warning shown.
- Rejects duplicate profile.
- Rejects invalid profile names.
- Rejects missing serial for `bambu-lan`.
- Rejects invalid serial format.
- Rejects missing access code in non-interactive mode.
- Rejects unsafe access-code file permissions.
- Fails with exit code `4` when TLS or network connection fails (without `--insecure`).
- Fails with exit code `3` when MQTT authentication is rejected.
- Fails closed when keychain is unavailable.
- Does not write access code or TLS fingerprint into YAML config.
- Emits stable JSON envelope for success and failure.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Fails before connecting when the protocol trace file cannot be created.
- Does not leak access code, raw auth payloads, raw MQTT payloads, TLS material, or keychain backend details in protocol trace output.
- Emits `insecure: false`, `tlsFingerprint`, and `serial` in JSON on secure success.
- Emits `insecure: true`, `tlsFingerprint: null`, and `serial` in JSON on insecure success.
- Rolls back access code keychain entry if TLS fingerprint write fails.
- Rolls back both keychain entries if config write fails.
- Includes serial in human and JSON success output.

## Non-goals

- Discovering printers.
- Accepting Bambu cloud credentials.
- Updating or rotating an existing access code.
- Overwriting an existing profile.
- Refreshing a TLS certificate fingerprint (use `printer tls refresh`).

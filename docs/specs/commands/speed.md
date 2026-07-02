# Command Spec: `speed`

## Status

Accepted

## Purpose

Set the active print speed profile on a configured printer through a
driver-neutral speed control operation.

The `speed` command group is a top-level command. It consumes printer profiles
created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and
ADR 0014 for auxiliary printer controls.

Reading current speed level is covered by `status --detailed`; this command
sets the active print speed profile for the current job.

## Syntax

```text
polimero speed set <printer> <profile> [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.
- `<profile>`: driver-supported speed profile token. Driver specs define the
  accepted canonical values. Common examples are `silent`, `standard`, `sport`,
  and `ludicrous`. Profile tokens are lowercase ASCII made of letters, digits,
  `_`, `-`, or `.`. The command layer does not case-fold profile names.

## Flags

- `--yes`: optional. Skips the interactive confirmation prompt. Required in
  non-interactive sessions (no controlling TTY).
- `--timeout <duration>`: optional. Overrides profile/default timeout for the
  status check and speed operation.
- `--insecure`: optional. Skips TLS verification for this invocation regardless
  of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol
  diagnostics to a new local file. The file must not already exist. Trace output
  may include status precheck phases, speed action phase names, capability
  decisions, selected portable states, acknowledged speed profile, byte counts,
  durations, parser warnings, and sanitized error categories. It must not
  include access codes, raw auth payloads, MQTT payloads containing credential
  material, TLS private material, or unsanitized protocol errors. Traced MQTT
  command and report payloads are secret-free per ADR 0013.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads the named profile from versioned YAML config under
`os.UserConfigDir`.

The profile must include:

- name
- driver
- host
- serial when required by the driver
- timeout or default timeout

The command does not write profile config.

## Secret Requirements

The command loads keychain entries using the driver name and profile name from
the stored profile:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint` (skipped when `--insecure`
  or `profile.insecure: true`)

Keychain reads use the same bounded command timeout as the speed operation.

If the access code is missing or keychain access fails, the command fails with
exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with
exit code `3`.

If the TLS fingerprint is present but empty or not formatted as
`sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Execution Order

1. Resolve profile and load secrets.
2. Validate `<profile>` syntax. It must be a non-empty lowercase ASCII token
   made of letters, digits, `_`, `-`, or `.`. Malformed profile syntax fails
   with exit code `2` and error code `invalid_argument` before any network call.
3. If `--protocol-trace` is set, create the trace file before any protocol work.
4. Query current status via the driver-neutral status operation. If status
   fails, return that auth, secret, transport, timeout, or parse failure as-is.
5. Check the state precondition: current state must be `printing` or `paused`.
   Fail with exit code `2` and error code `invalid_printer_state` otherwise.
6. Prompt for confirmation unless `--yes` is set.
7. Dispatch the speed profile to the driver. The driver blocks, bounded by
   `--timeout`, until it confirms the printer acknowledged the requested
   profile.
8. Close the trace file if one was opened, then render the result.

## Confirmation

Changing print speed is state-changing and requires confirmation by default:

- Without `--yes`, in an interactive session, the command prompts:
  `Set speed profile <profile> on <printer>? Type 'yes' to continue: `.
- Any answer other than `yes` fails with exit code `2`.
- Without `--yes`, in a non-interactive session, the command fails immediately
  with exit code `2`.
- `--yes` skips the prompt entirely.

## Behavior

- Default timeout is `10s`, applied to both the status check and speed operation.
- No retry is performed by default.
- This command controls the active print speed profile only.
- The command does not accept arbitrary percentage multipliers, acceleration
  settings, jerk settings, or motion feedrates.
- The driver must confirm the requested profile before success. A fresh status
  echo is preferred. A protocol acknowledgment is acceptable only when it
  positively identifies the requested speed profile. Publish or socket-write
  success alone is not success.
- If no acknowledgment arrives before the timeout, the command fails with exit
  code `4` and error code `timeout`.
- Unsupported speed-control capability or unsupported profile fails with exit
  code `5`.
- If the protocol trace file cannot be created, the command fails before
  protocol work with exit code `2`. If trace writing or closing fails after
  protocol work starts, the command fails with exit code `1` unless an earlier,
  more specific failure already occurred.

## Data Contracts

Speed result fields:

- `profile`: profile name.
- `driver`: driver name.
- `speedProfile`: acknowledged speed profile token.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human example:

```text
Printer: garage-x1c
Speed profile set to sport.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "speedProfile": "sport",
    "warnings": [],
    "capabilities": {
      "speedControl": true
    }
  },
  "error": null,
  "meta": {
    "command": "speed set",
    "durationMs": 260
  }
}
```

JSON precondition error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "invalid_printer_state",
    "message": "cannot set speed: printer is not printing or paused",
    "details": {
      "profile": "garage-x1c",
      "currentState": "idle",
      "requiredState": "printing_or_paused"
    }
  },
  "meta": {
    "command": "speed set"
  }
}
```

## Exit Codes

- `0`: speed profile acknowledged.
- `1`: general failure, including trace write or close failure after protocol
  work starts.
- `2`: usage, profile, config, malformed profile token, precondition,
  confirmation error, or invalid/uncreatable protocol trace path before protocol
  work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability, including an unsupported speed profile for the
  connected model.

## Error Cases

- Missing `<printer>`.
- Missing `<profile>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Invalid speed profile token syntax.
- State precondition not met.
- Confirmation declined.
- Non-interactive session without `--yes`.
- Protocol trace path already exists or cannot be created.
- Access code not found in keychain.
- TLS fingerprint not found in keychain (secure profile).
- TLS fingerprint invalid in keychain (secure profile).
- TLS fingerprint mismatch (TOFU violation).
- Authentication failed.
- Connection failed.
- Timeout waiting for acknowledgment.
- Driver does not support speed control.
- Connected model does not support the requested speed profile.

## Security Requirements

- Do not print or log access codes.
- Never bypass input validation, state precondition check, or confirmation prompt
  regardless of `--output` format.
- `--yes` only skips the prompt; it does not skip validation or preconditions.
- Sanitize authentication, transport, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Protocol trace output must contain sanitized speed-control events; traced MQTT payloads must be secret-free (ADR 0013).
- Do not perform discovery or scanning.

## Test Scenarios

- Sets a supported speed profile while printing.
- Sets a supported speed profile while paused.
- Rejects uppercase or otherwise malformed profile tokens with
  `invalid_argument`.
- Fails with `invalid_printer_state` while idle.
- Fails with `invalid_printer_state` while offline, unknown, or error.
- Returns status precheck errors without prompting or dispatching speed control.
- Fails with exit code `2` when confirmation is declined.
- Fails with exit code `2` in a non-interactive session without `--yes`.
- Skips confirmation when `--yes` is set.
- Fails with `timeout` when no acknowledgment arrives in time.
- Fails with unsupported capability for a driver without speed control.
- Fails with unsupported capability for an unsupported speed profile.
- Uses command timeout override for status and speed operation.
- Emits stable JSON envelope.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Does not leak secrets in output, logs, or trace; raw protocol payloads stay out of output and logs, and traced payloads are secret-free.

## Non-goals

- Reading current speed level; see `status --detailed`.
- Arbitrary percentage multipliers.
- Acceleration, jerk, pressure advance, flow, or motion feedrate settings.
- Heater, fan, light, motion, job, or AMS control.
- Discovering printers.

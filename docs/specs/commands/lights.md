# Command Spec: `lights`

## Status

Accepted

## Purpose

Turn simple printer lights on or off on a configured printer through a
driver-neutral light control operation.

The `lights` command group is a top-level command. It consumes printer profiles
created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and
ADR 0014 for auxiliary printer controls.

Reading current light state is covered by `status --detailed`; this command only
sets one light target per invocation.

## Syntax

```text
polimero lights set <printer> <light> <state> [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.
- `<light>`: driver-supported light key. Portable aliases `chamber`,
  `chamber-light`, and `chamber_light` are normalized to canonical key
  `chamber` before driver dispatch. Driver specs may define additional safe keys
  or aliases. The token must be non-empty ASCII made of letters, digits, `_`,
  `-`, or `.`.
- `<state>`: `on` or `off`.

## Flags

- `--yes`: optional. Skips the interactive confirmation prompt. Required in
  non-interactive sessions (no controlling TTY).
- `--timeout <duration>`: optional. Overrides profile/default timeout for the
  status check and light operation.
- `--insecure`: optional. Skips TLS verification for this invocation regardless
  of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol
  diagnostics to a new local file. The file must not already exist. Trace output
  may include status precheck phases, light action phase names, capability
  decisions, selected portable states, acknowledged light key and state, byte
  counts, durations, parser warnings, and sanitized error categories. It must
  not include access codes, raw auth payloads, raw MQTT payloads, raw command
  payloads, TLS private material, or unsanitized protocol errors.
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

Keychain reads use the same bounded command timeout as the light operation.

If the access code is missing or keychain access fails, the command fails with
exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with
exit code `3`.

If the TLS fingerprint is present but empty or not formatted as
`sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Execution Order

1. Resolve profile and load secrets.
2. Validate `<light>` syntax, normalize portable aliases, and validate
   `<state>`. Malformed light syntax or invalid state fails with exit code `2`
   and error code `invalid_argument` before any network call.
3. If `--protocol-trace` is set, create the trace file before any protocol work.
4. Query current status via the driver-neutral status operation. If status
   fails, return that auth, secret, transport, timeout, or parse failure as-is.
5. Check the state precondition: current state must be known and reachable.
   Allowed states are `idle`, `printing`, `paused`, and `error`. Fail with exit
   code `2` and error code `invalid_printer_state` otherwise.
6. Prompt for confirmation unless `--yes` is set.
7. Dispatch the light target to the driver. The driver blocks, bounded by
   `--timeout`, until it confirms the printer acknowledged the requested state.
8. Close the trace file if one was opened, then render the result.

## Confirmation

Changing a light is state-changing and requires confirmation by default:

- Without `--yes`, in an interactive session, the command prompts:
  `Set <light> light <state> on <printer>? Type 'yes' to continue: `.
- Any answer other than `yes` fails with exit code `2`.
- Without `--yes`, in a non-interactive session, the command fails immediately
  with exit code `2`.
- `--yes` skips the prompt entirely.

## Behavior

- Default timeout is `10s`, applied to both the status check and light operation.
- No retry is performed by default.
- One light is set per command invocation.
- The driver receives the canonical light key, not a CLI alias.
- Only `on` and `off` are supported by this command.
- Brightness, color, and animation controls are out of scope.
- The driver must confirm the requested light state before success. A fresh
  status echo is preferred. A protocol acknowledgment is acceptable only when it
  positively identifies the requested light and requested state. Publish or
  socket-write success alone is not success.
- If no acknowledgment arrives before the timeout, the command fails with exit
  code `4` and error code `timeout`.
- Unsupported light-control capability or unsupported light key fails with exit
  code `5`.
- If the protocol trace file cannot be created, the command fails before
  protocol work with exit code `2`. If trace writing or closing fails after
  protocol work starts, the command fails with exit code `1` unless an earlier,
  more specific failure already occurred.

## Data Contracts

Light result fields:

- `profile`: profile name.
- `driver`: driver name.
- `light`: canonical light key returned by the driver, for example `chamber`.
- `state`: acknowledged state, `on` or `off`.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human example:

```text
Printer: garage-x1c
Chamber light set to on.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "light": "chamber",
    "state": "on",
    "warnings": [],
    "capabilities": {
      "lightControl": true
    }
  },
  "error": null,
  "meta": {
    "command": "lights set",
    "durationMs": 220
  }
}
```

JSON invalid-state error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "invalid_printer_state",
    "message": "cannot set light: printer state is unknown",
    "details": {
      "profile": "garage-x1c",
      "currentState": "unknown",
      "requiredState": "known"
    }
  },
  "meta": {
    "command": "lights set"
  }
}
```

## Exit Codes

- `0`: light target acknowledged.
- `1`: general failure, including trace write or close failure after protocol
  work starts.
- `2`: usage, profile, config, malformed token, precondition, confirmation
  error, invalid state value, or invalid/uncreatable protocol trace path before
  protocol work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability, including an unsupported light key for the
  connected model.

## Error Cases

- Missing `<printer>`.
- Missing `<light>`.
- Missing `<state>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Invalid light token syntax.
- Invalid `<state>` value.
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
- Driver does not support light control.
- Connected model does not support the requested light.

## Security Requirements

- Do not print or log access codes.
- Never bypass input validation, state precondition check, or confirmation prompt
  regardless of `--output` format.
- `--yes` only skips the prompt; it does not skip validation or preconditions.
- Sanitize authentication, transport, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Protocol trace output must contain sanitized light-control summaries only.
- Do not perform discovery or scanning.

## Test Scenarios

- Sets chamber light on.
- Sets chamber light off.
- Normalizes `chamber-light` and `chamber_light` to the canonical `chamber`
  result key.
- Fails with `invalid_argument` for malformed light syntax.
- Fails with `invalid_argument` for an invalid state value.
- Returns status precheck errors without prompting or dispatching light control.
- Allows current state `idle`, `printing`, `paused`, and `error`.
- Fails with `invalid_printer_state` when current state is `offline` or
  `unknown`.
- Fails with exit code `2` when confirmation is declined.
- Fails with exit code `2` in a non-interactive session without `--yes`.
- Skips confirmation when `--yes` is set.
- Fails with `timeout` when no acknowledgment arrives in time.
- Fails with unsupported capability for a driver without light control.
- Fails with unsupported capability for an unsupported light key.
- Uses command timeout override for status and light operation.
- Emits stable JSON envelope.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Does not leak secrets or raw protocol payloads in output, logs, or trace.

## Non-goals

- Reading light state; see `status --detailed`.
- Brightness, color, animation, or schedule control.
- Camera lighting automation or image-triggered lighting.
- Fan, speed, heater, motion, job, or AMS control.
- Discovering printers.

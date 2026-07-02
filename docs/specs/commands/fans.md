# Command Spec: `fans`

## Status

Accepted

## Purpose

Set user-controllable printer fan speeds on a configured printer through a
driver-neutral fan control operation.

The `fans` command group is a top-level command. It consumes printer profiles
created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and
ADR 0014 for auxiliary printer controls.

Reading current fan speeds is covered by `status --detailed`; this command only
sets one fan target per invocation.

## Syntax

```text
polimero fans set <printer> <fan> <percent> [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.
- `<fan>`: driver-supported, user-controllable fan key. Portable aliases are
  normalized before driver dispatch:

  | Input aliases | Canonical key |
  |---|---|
  | `part-cooling`, `part_cooling`, `partCooling` | `partCooling` |
  | `aux`, `auxiliary` | `auxiliary` |
  | `chamber` | `chamber` |

  Driver specs may define additional safe keys and aliases. The token must be
  non-empty ASCII made of letters, digits, `_`, `-`, or `.`.
- `<percent>`: integer fan speed percentage from `0` to `100`. `0` turns the fan
  off.

## Flags

- `--yes`: optional. Skips the interactive confirmation prompt. Required in
  non-interactive sessions (no controlling TTY).
- `--timeout <duration>`: optional. Overrides profile/default timeout for the
  status check and fan operation.
- `--insecure`: optional. Skips TLS verification for this invocation regardless
  of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol
  diagnostics to a new local file. The file must not already exist. Trace output
  may include status precheck phases, fan action phase names, capability
  decisions, selected portable states, acknowledged fan key and percentage,
  byte counts, durations, parser warnings, and sanitized error categories. It
  must not include access codes, raw auth payloads, MQTT payloads containing
  credential material, TLS private material, or unsanitized protocol errors.
  Traced MQTT command and report payloads are secret-free per ADR 0013.
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

Keychain reads use the same bounded command timeout as the fan operation.

If the access code is missing or keychain access fails, the command fails with
exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with
exit code `3`.

If the TLS fingerprint is present but empty or not formatted as
`sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Execution Order

1. Resolve profile and load secrets.
2. Validate `<fan>` syntax, normalize portable aliases, and validate
   `<percent>` bounds. Malformed fan syntax or malformed percent syntax fails
   with exit code `2` and error code `invalid_argument` before any network
   call. Out-of-range percent fails with exit code `2` and error code
   `unsafe_value` before any network call.
3. If `--protocol-trace` is set, create the trace file before any protocol work.
4. Query current status via the driver-neutral status operation. If status
   fails, return that auth, secret, transport, timeout, or parse failure as-is.
5. Check the state precondition: current state must be `idle`, `printing`, or
   `paused`. Fail with exit code `2` and error code `invalid_printer_state`
   otherwise.
6. Prompt for confirmation unless `--yes` is set.
7. Dispatch the fan target to the driver. The driver blocks, bounded by
   `--timeout`, until it confirms the printer acknowledged the requested speed.
8. Close the trace file if one was opened, then render the result.

## Safety Bounds

Enforced by the command layer before any network call:

| Target | Minimum | Maximum |
|---|---:|---:|
| Fan speed | 0% | 100% |

The generic command must not expose firmware-managed safety fans. Heatbreak,
hotend, electronics, controller, and power-supply fans are out of scope unless a
later accepted ADR and driver spec explicitly add them.

## Confirmation

Setting a fan speed is state-changing and requires confirmation by default:

- Without `--yes`, in an interactive session, the command prompts:
  `Set <fan> fan to <percent>% on <printer>? Type 'yes' to continue: `.
- Any answer other than `yes` fails with exit code `2`.
- Without `--yes`, in a non-interactive session, the command fails immediately
  with exit code `2`.
- `--yes` skips the prompt entirely.

## Behavior

- Default timeout is `10s`, applied to both the status check and fan operation.
- No retry is performed by default.
- One fan is set per command invocation.
- The driver receives the canonical fan key, not a CLI alias.
- `0` means off. `100` means the maximum safe user-controllable speed exposed by
  the driver.
- The driver must confirm the requested fan speed before success. A fresh status
  echo is preferred. A protocol acknowledgment is acceptable only when it
  positively identifies the requested fan and requested percentage. Publish or
  socket-write success alone is not success.
- If no acknowledgment arrives before the timeout, the command fails with exit
  code `4` and error code `timeout`.
- Unsupported fan-control capability or unsupported fan key fails with exit code
  `5`.
- If the protocol trace file cannot be created, the command fails before
  protocol work with exit code `2`. If trace writing or closing fails after
  protocol work starts, the command fails with exit code `1` unless an earlier,
  more specific failure already occurred.

## Data Contracts

Fan result fields:

- `profile`: profile name.
- `driver`: driver name.
- `fan`: canonical fan key returned by the driver, for example `partCooling`.
- `speedPercent`: acknowledged speed percentage.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human example:

```text
Printer: garage-x1c
Part cooling fan set to 60%.
```

Human confirmation prompt:

```text
Set part-cooling fan to 60% on garage-x1c? Type 'yes' to continue: yes
Printer: garage-x1c
Part cooling fan set to 60%.
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "fan": "partCooling",
    "speedPercent": 60,
    "warnings": [],
    "capabilities": {
      "fanControl": true
    }
  },
  "error": null,
  "meta": {
    "command": "fans set",
    "durationMs": 340
  }
}
```

JSON bounds error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "unsafe_value",
    "message": "fan speed out of range",
    "details": {
      "fan": "part-cooling",
      "value": 120,
      "minimum": 0,
      "maximum": 100
    }
  },
  "meta": {
    "command": "fans set"
  }
}
```

## Exit Codes

- `0`: fan target acknowledged.
- `1`: general failure, including trace write or close failure after protocol
  work starts.
- `2`: usage, profile, config, malformed token, bounds, precondition,
  confirmation error, or invalid/uncreatable protocol trace path before
  protocol work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability, including an unsupported fan key for the
  connected model.

## Error Cases

- Missing `<printer>`.
- Missing `<fan>`.
- Missing `<percent>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- Invalid fan token syntax.
- Invalid percent token syntax.
- Fan speed outside `0` to `100`.
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
- Driver does not support fan control.
- Connected model does not support the requested fan.

## Security Requirements

- Do not print or log access codes.
- Never bypass the safety bounds check, state precondition check, or
  confirmation prompt, regardless of `--output` format.
- `--yes` only skips the prompt; it does not skip bounds or preconditions.
- Sanitize authentication, transport, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Protocol trace output must contain sanitized fan-control events; traced MQTT payloads must be secret-free (ADR 0013).
- Do not perform discovery or scanning.
- Do not expose firmware-managed safety fan overrides through this command.

## Test Scenarios

- Sets part-cooling fan to a non-zero speed.
- Normalizes `part-cooling` to the canonical `partCooling` result key.
- Sets a supported fan to `0`.
- Sets a supported fan to `100`.
- Fails with `invalid_argument` for malformed fan syntax.
- Fails with `invalid_argument` for malformed percent syntax.
- Fails with `unsafe_value` below `0` or above `100`.
- Returns status precheck errors without prompting or dispatching fan control.
- Fails with `invalid_printer_state` when current state is `offline`,
  `unknown`, or `error`.
- Allows current state `idle`, `printing`, and `paused`.
- Fails with exit code `2` when confirmation is declined.
- Fails with exit code `2` in a non-interactive session without `--yes`.
- Skips confirmation when `--yes` is set.
- Fails with `timeout` when no acknowledgment arrives in time.
- Fails with unsupported capability for a driver without fan control.
- Fails with unsupported capability for an unsupported fan key.
- Uses command timeout override for status and fan operation.
- Emits stable JSON envelope.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Does not leak secrets in output, logs, or trace; raw protocol payloads stay out of output and logs, and traced payloads are secret-free.

## Non-goals

- Reading fan speeds; see `status --detailed`.
- Fan curves, per-layer scheduling, or automatic material profiles.
- Firmware-managed safety fan overrides.
- Arbitrary G-code fan commands.
- Heater, motion, job, light, speed, or AMS control.
- Discovering printers.

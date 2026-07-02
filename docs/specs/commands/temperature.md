# Command Spec: `temperature`

## Status

Accepted

## Purpose

Set heater target temperatures on a configured printer through a driver-neutral temperature control operation.

The `temperature` command group is a top-level command. It consumes printer profiles created and managed by `printer`.

This command is covered by ADR 0008 for top-level action command placement and ADR 0012 for printer control commands.

Reading current temperatures is already covered by `status`; this command only sets targets.

## Syntax

```text
polimero temperature set <printer> [--nozzle <celsius>] [--bed <celsius>] [--chamber <celsius>] [--yes] [--timeout <duration>] [--insecure] [--protocol-trace <file>] [--output <format>]
```

## Arguments

- `<printer>`: existing printer profile name.

## Flags

- `--nozzle <celsius>`: optional. Target nozzle temperature. Range: `0`–`300`. `0` turns the heater off.
- `--bed <celsius>`: optional. Target bed temperature. Range: `0`–`120`. `0` turns the heater off.
- `--chamber <celsius>`: optional. Target chamber temperature. Range: `0`–`65`. `0` turns the heater off. Fails with exit code `5` if the connected printer has no chamber heater.
- At least one of `--nozzle`, `--bed`, `--chamber` is required.
- `--yes`: optional. Skips the interactive confirmation prompt. Required in non-interactive sessions (no controlling TTY).
- `--timeout <duration>`: optional. Overrides profile/default timeout for the status check and the temperature operation.
- `--insecure`: optional. Skips TLS verification for this invocation regardless of the profile `insecure` setting.
- `--protocol-trace <file>`: optional. Writes sanitized JSON Lines protocol diagnostics to a new local file. The file must not already exist. Trace output may include status precheck phases, temperature action phase names, capability decisions, selected portable states, acknowledged target categories, byte counts, durations, parser warnings, and sanitized error categories. It must not include access codes, raw auth payloads, MQTT payloads containing credential material, TLS private material, or unsanitized protocol errors. Traced MQTT command and report payloads are secret-free per ADR 0013.
- `--output <format>`: global flag. Values: `human`, `json`. Default: `human`.

## Config Requirements

The command reads the named profile from versioned YAML config under `os.UserConfigDir`.

The profile must include:

- name
- driver
- host
- serial when required by the driver
- timeout or default timeout

The command does not write profile config.

## Secret Requirements

The command loads keychain entries using the driver name and profile name from the stored profile:

- Access code: `<driver>:<name>:access-code`
- TLS fingerprint: `<driver>:<name>:tls-fingerprint` (skipped when `--insecure` or `profile.insecure: true`)

Keychain reads use the same bounded command timeout as the temperature operation.

If the access code is missing or keychain access fails, the command fails with exit code `3`.

If the TLS fingerprint is missing for a secure profile, the command fails with exit code `3`.

If the TLS fingerprint is present but empty or not formatted as `sha256:<64 lowercase hex characters>`, the command fails with exit code `3`.

The command must not prompt for new secrets.

## Execution Order

1. Resolve profile and load secrets.
2. Validate that at least one target flag was given and that each given value is within its safety bound (exit code `2`, error code `unsafe_value`, on failure; no network call).
3. If `--protocol-trace` is set, create the trace file before any protocol work.
4. Query current status via the driver-neutral status operation.
5. Check the state precondition: current state must be `idle`. Fail with exit code `2` and error code `invalid_printer_state` otherwise.
6. Prompt for confirmation unless `--yes` is set.
7. Dispatch the temperature targets to the driver. The driver blocks (bounded by `--timeout`) until it confirms the printer acknowledged the new target value(s).
8. Close the trace file if one was opened, then render the result.

## Safety Bounds

Enforced by the command layer before any network call, independent of whatever limits the printer firmware itself enforces:

| Target | Minimum | Maximum |
|---|---|---|
| Nozzle | 0°C | 300°C |
| Bed | 0°C | 120°C |
| Chamber | 0°C | 65°C |

A value outside these bounds fails with exit code `2` and error code `unsafe_value`, listing the offending target and value in `error.details`.

## Confirmation

Setting a temperature target is state-changing and requires confirmation by default:

- Without `--yes`, in an interactive session, the command prompts with a summary of the requested targets: `Set <targets> on <printer>? Type 'yes' to continue: `. Any answer other than `yes` fails with exit code `2`.
- Without `--yes`, in a non-interactive session (no controlling TTY), the command fails immediately with exit code `2`.
- `--yes` skips the prompt entirely.

## Behavior

- Default timeout is `10s`, applied to both the status check and the temperature operation.
- No retry is performed by default.
- Multiple targets may be set in a single call (e.g. `--nozzle 220 --bed 60` together); they are sent as one request and confirmed together.
- The command waits for the printer to acknowledge the requested target value(s) for every target given, not for the current temperature to reach target. Reaching target is a separate, asynchronous process the operator can observe via `status`.
- `TemperatureWrite: true` indicates the driver supports setting temperatures generally. If the connected model lacks a specific heater (most commonly chamber), the command fails with exit code `5` for that target even though the driver advertises `TemperatureWrite: true`.
- If no acknowledgment arrives before the timeout, the command fails with exit code `4` and error code `timeout`.
- Unsupported driver capabilities fail with exit code `5`.
- If the protocol trace file cannot be created, the command fails before protocol work with exit code `2`. If trace writing or closing fails after protocol work starts, the command fails with exit code `1` unless an earlier, more specific failure already occurred.

## Data Contracts

Temperature result fields:

- `profile`: profile name.
- `driver`: driver name.
- `targets`: object with `nozzleCelsius`, `bedCelsius`, `chamberCelsius`. Each is the acknowledged value when that target was requested, or `null` when not requested.
- `warnings`: non-fatal warnings. Always an array; empty when none.
- `capabilities`: driver capability metadata. Always present.

## Output

Human example:

```text
Printer: garage-x1c
Nozzle target set to 220.0 C
Bed target set to 60.0 C
```

Human confirmation prompt (interactive, no `--yes`):

```text
Set nozzle=220.0C, bed=60.0C on garage-x1c? Type 'yes' to continue: yes
Printer: garage-x1c
Nozzle target set to 220.0 C
Bed target set to 60.0 C
```

JSON success example:

```json
{
  "ok": true,
  "data": {
    "profile": "garage-x1c",
    "driver": "bambu-lan",
    "targets": {
      "nozzleCelsius": 220.0,
      "bedCelsius": 60.0,
      "chamberCelsius": null
    },
    "warnings": [],
    "capabilities": {
      "temperatureWrite": true
    }
  },
  "error": null,
  "meta": {
    "command": "temperature set",
    "durationMs": 340
  }
}
```

When `--protocol-trace` is enabled and the trace file is opened successfully, JSON `meta` may include `protocolTracePath`. Human output never includes trace contents.

JSON bounds error example:

```json
{
  "ok": false,
  "data": null,
  "error": {
    "code": "unsafe_value",
    "message": "nozzle target out of range",
    "details": {
      "target": "nozzle",
      "value": 350.0,
      "minimum": 0.0,
      "maximum": 300.0
    }
  },
  "meta": {
    "command": "temperature set"
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
    "message": "cannot set temperature while printing",
    "details": {
      "profile": "garage-x1c",
      "currentState": "printing",
      "requiredState": "idle"
    }
  },
  "meta": {
    "command": "temperature set"
  }
}
```

## Exit Codes

- `0`: temperature targets acknowledged.
- `1`: general failure, including trace write or close failure after protocol work starts.
- `2`: usage, profile, config, bounds, precondition, confirmation error, or invalid/uncreatable protocol trace path before protocol work starts.
- `3`: auth or secret error.
- `4`: network or timeout error.
- `5`: unsupported capability, including a target unsupported by the connected model.

## Error Cases

- Missing `<printer>`.
- Invalid profile name.
- Profile not found.
- Config schema version is not `1`.
- No target flag given.
- Target value outside its safety bound.
- State precondition not met (printer not idle).
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
- Driver does not support temperature control.
- Connected model does not support a requested target (e.g. chamber).

## Security Requirements

- Do not print or log access codes.
- Never bypass the safety bounds check, the state precondition check, or the confirmation prompt, regardless of `--output` format.
- `--yes` only skips the interactive prompt; it does not skip the bounds or precondition checks.
- Sanitize authentication, transport, and secret-store errors.
- Sanitize protocol parser errors and do not expose raw protocol payloads.
- Protocol trace output must contain sanitized temperature-control events. It must not include access codes, raw auth payloads, MQTT payloads containing credential material, TLS private material, or unsanitized protocol errors. Traced MQTT command and report payloads are secret-free per ADR 0013.
- Do not perform discovery or scanning.

## Test Scenarios

- Sets nozzle target only; confirms acknowledged value.
- Sets bed target only; confirms acknowledged value.
- Sets nozzle and bed together in one call; confirms both acknowledged.
- Sets chamber target on a driver/model that supports it.
- Fails with exit code `5` for chamber target on a model without a chamber heater.
- Sets target to `0` to turn off a heater.
- Fails with `unsafe_value` for a nozzle value above 300 or below 0.
- Fails with `unsafe_value` for a bed value above 120 or below 0.
- Fails with `unsafe_value` for a chamber value above 65 or below 0.
- Fails with `unsafe_value` for a non-finite target value (`NaN`, `Inf`).
- Fails with exit code `2` when no target flag is given.
- Fails with `invalid_printer_state` when current state is not `idle`.
- Fails with exit code `2` when confirmation is declined.
- Fails with exit code `2` in a non-interactive session without `--yes`.
- Skips confirmation when `--yes` is set.
- Fails with `timeout` when no acknowledgment arrives in time.
- Fails when profile is missing.
- Fails when access code is missing from keychain.
- Fails when TLS fingerprint is missing for a secure profile.
- Fails with exit code `3` on TLS fingerprint mismatch.
- Skips TLS fingerprint check when profile has `insecure: true`.
- Skips TLS fingerprint check when `--insecure` flag is passed.
- Fails with unsupported capability for a driver that does not support temperature control.
- Uses command timeout override for the status check and the temperature operation.
- Emits stable JSON envelope.
- Writes sanitized protocol trace events when `--protocol-trace` is set.
- Refuses to overwrite an existing protocol trace file.
- Fails before protocol work when the protocol trace file cannot be created.
- Does not leak access codes, raw auth payloads, credential material, or TLS material in protocol trace output.
- Does not leak secrets in output or logs.

## Non-goals

- Reading current temperatures; see `status`.
- Waiting for the temperature to reach target.
- Setting temperatures while printing or paused.
- Controlling fans or other non-heater outputs.
- Per-extruder temperature control on multi-extruder printers.
- Discovering printers.
